package bridge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"zeussdd/internal/acp"
)

func TestTaskChatACP(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "GigaCode & percent%")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(dir, "gigacode")
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(executable, "'", "'\\''") + "' -test.run=^TestAgentFixture$ -- \"$@\"\n"
	if runtime.GOOS == "windows" {
		launcher += ".cmd"
		script = fmt.Sprintf("@echo off\r\n\"%s\" -test.run=TestAgentFixture -- %%*\r\n", executable)
		t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	}
	if err := os.WriteFile(launcher, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	for _, override := range []string{launcher, ""} {
		name := "full_path"
		if override == "" {
			name = "PATH"
		}
		t.Run(name, func(t *testing.T) {
			testTaskChat(t, acp.Profile{Home: filepath.Join(root, "profile"), WorkDir: root, Launcher: override})
		})
	}
}

func testTaskChat(t *testing.T, profile acp.Profile) {
	t.Helper()
	server := NewServer(Config{Profile: profile, MCPURL: "http://example.test/mcp"})
	handler := server.Routes()
	start := acpHTTP(handler, "POST", "/api/acp/sessions", "{\"taskKey\":\"ZEUS-214\"}", "")
	var session struct{ SessionID string }
	if err := json.Unmarshal(start.Body.Bytes(), &session); err != nil || start.Code != 201 || session.SessionID == "" {
		t.Fatalf("start: %d %s", start.Code, start.Body)
	}
	url := "/api/acp/sessions/" + session.SessionID
	server.mu.RLock()
	active := server.sessions[session.SessionID]
	server.mu.RUnlock()
	if active == nil {
		t.Fatal("agent exited after session creation")
	}
	t.Cleanup(func() {
		active.stop()
		await(t, func() bool {
			server.mu.RLock()
			defer server.mu.RUnlock()
			return server.sessions[session.SessionID] == nil
		})
	})

	prompt := func(text string) <-chan *httptest.ResponseRecorder {
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			done <- acpHTTP(handler, "POST", url+"/prompt", fmt.Sprintf("{\"text\":%q}", text), "")
		}()
		return done
	}
	waitPermission := func() string {
		var id string
		await(t, func() bool {
			active.mu.Lock()
			defer active.mu.Unlock()
			for pending := range active.pending {
				id = pending
			}
			return id != ""
		})
		return id
	}
	waitResult := func(ch <-chan *httptest.ResponseRecorder, stopReason string) {
		t.Helper()
		select {
		case response := <-ch:
			if response.Code != 200 || !strings.Contains(response.Body.String(), stopReason) {
				t.Fatalf("prompt: %d %s", response.Code, response.Body)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("prompt response timed out")
		}
	}

	first := prompt("первый")
	id := waitPermission()
	select {
	case response := <-first:
		t.Fatalf("permission answered automatically: %d %s", response.Code, response.Body)
	default:
	}
	invalid := acpHTTP(handler, "POST", url+"/permissions", fmt.Sprintf("{\"id\":%q,\"optionId\":\"invented\"}", id), "")
	if invalid.Code != 400 {
		t.Fatalf("invalid permission option: %d", invalid.Code)
	}
	approved := acpHTTP(handler, "POST", url+"/permissions", fmt.Sprintf("{\"id\":%q,\"optionId\":\"once\"}", id), "")
	if approved.Code != 204 {
		t.Fatalf("permission: %d %s", approved.Code, approved.Body)
	}
	waitResult(first, "end_turn")

	second := prompt("второй")
	waitPermission()
	other := acpHTTP(handler, "POST", url+"/prompt", "{\"text\":\"too soon\"}", "")
	if other.Code != 409 {
		t.Fatalf("parallel prompt: %d", other.Code)
	}
	cancelled := acpHTTP(handler, "POST", url+"/cancel", "", "")
	if cancelled.Code != 204 {
		t.Fatalf("cancel: %d %s", cancelled.Code, cancelled.Body)
	}
	waitResult(second, "cancelled")

	// Replay a stream captured before session teardown, starting after chunk 67.
	// The 80 chunks and acknowledgements must still be in publication order.
	closed := acpHTTP(handler, "DELETE", url, "", "")
	if closed.Code != 204 {
		t.Fatalf("close: %d", closed.Code)
	}
	await(t, func() bool {
		server.mu.RLock()
		defer server.mu.RUnlock()
		return server.sessions[session.SessionID] == nil
	})
	replay := acpHTTP(handler, "GET", url+"/events", "", "68")
	body := replay.Body.String()
	if replay.Code != 200 || !strings.HasPrefix(body, "id: 69\n") ||
		!strings.Contains(body, "chunk-79") || !strings.Contains(body, "\"kind\":\"closed\"") {
		t.Fatalf("SSE replay: %d %s", replay.Code, body)
	}
}

func acpHTTP(handler http.Handler, method, target, body, cursor string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost"+target, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Last-Event-ID", cursor)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func await(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for ACP")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAgentFixture(t *testing.T) {
	index := slices.Index(os.Args, "--")
	if index < 0 {
		return
	}
	if !slices.Equal(os.Args[index+1:], []string{"--acp", "--approval-mode", "default"}) {
		t.Fatalf("unexpected launcher flags: %q", os.Args[index+1:])
	}
	reader := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	send := func(fields map[string]any) {
		fields["jsonrpc"] = "2.0"
		if err := encoder.Encode(fields); err != nil {
			t.Fatal(err)
		}
	}
	reply := func(id any, fields map[string]any) { send(map[string]any{"id": id, "result": fields}) }
	var promptID any
	var cancelled bool
	for reader.Scan() {
		var message struct {
			ID     any
			Method string
			Params struct {
				SessionID  string
				Prompt     []struct{ Text string }
				MCPServers []map[string]any
			}
			Result struct{ Outcome acp.PermissionOutcome }
		}
		if err := json.Unmarshal(reader.Bytes(), &message); err != nil {
			t.Fatal(err)
		}
		switch message.Method {
		case "initialize":
			reply(message.ID, map[string]any{
				"protocolVersion":   1,
				"agentCapabilities": map[string]any{"mcpCapabilities": map[string]bool{"http": true}},
			})
		case "session/new":
			if len(message.Params.MCPServers) != 1 || message.Params.MCPServers[0]["type"] != "http" {
				t.Fatalf("invalid MCP settings: %+v", message.Params.MCPServers)
			}
			reply(message.ID, map[string]any{"sessionId": "fixture"})
		case "session/prompt":
			prompt := message.Params.Prompt[0].Text
			if promptID == nil && (!strings.Contains(prompt, "ZEUS-214") || !strings.HasSuffix(prompt, "первый")) {
				t.Fatalf("missing task context: %q", prompt)
			}
			if promptID != nil && prompt != "второй" {
				t.Fatalf("unexpected follow-up: %q", prompt)
			}
			promptID = message.ID
			if strings.HasSuffix(prompt, "первый") {
				for i := 0; i < 80; i++ {
					send(map[string]any{"method": "session/update", "params": map[string]any{
						"sessionId": "fixture", "update": map[string]any{"sessionUpdate": "agent_message_chunk",
							"content": map[string]string{"type": "text", "text": fmt.Sprintf("chunk-%d", i)}},
					}})
				}
			}
			send(map[string]any{"id": "permission", "method": "session/request_permission", "params": map[string]any{
				"sessionId": "fixture", "options": []acp.PermissionOption{
					{ID: "once", Name: "Allow once", Kind: "allow_once"},
					{ID: "always", Name: "Allow always", Kind: "allow_always"},
				},
			}})
		case "session/cancel":
			cancelled = true
		case "":
			outcome := message.Result.Outcome
			switch {
			case cancelled && outcome.Outcome == "cancelled":
				reply(promptID, map[string]any{"stopReason": "cancelled"})
			case !cancelled && outcome.Outcome == "selected" && outcome.OptionID == "once":
				reply(promptID, map[string]any{"stopReason": "end_turn"})
			default:
				t.Fatalf("unexpected permission reply: %+v", outcome)
			}
		}
	}
	if err := reader.Err(); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}
