package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeAgent replays the wire format captured from a live GigaCode 26.10.109
// ACP session, so the client loop is exercised without spawning a process.
func fakeAgent(t *testing.T, toClient net.Conn, fromClient *bufio.Reader) {
	t.Helper()
	write := func(body string) {
		if _, err := toClient.Write([]byte(body + "\n")); err != nil {
			return
		}
	}
	for {
		line, err := fromClient.ReadString('\n')
		if err != nil {
			return
		}
		var message map[string]any
		if json.Unmarshal([]byte(line), &message) != nil {
			continue
		}
		if message["method"] == nil {
			// Response to the permission request we issued below.
			if message["id"] == "perm" {
				result, _ := message["result"].(map[string]any)
				outcome, _ := result["outcome"].(map[string]any)
				if outcome["optionId"] == "once" {
					write(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s-1","update":{"sessionUpdate":"tool_call","title":"approved"}}}`)
				}
			}
			continue
		}
		rawID, _ := json.Marshal(message["id"])
		switch message["method"] {
		case "initialize":
			write(`{"jsonrpc":"2.0","id":` + string(rawID) + `,"result":{"protocolVersion":1,"agentInfo":{"name":"qwen-code","title":"GigaCode","version":"26.10.109"}}}`)
		case "session/new":
			write(`{"jsonrpc":"2.0","id":` + string(rawID) + `,"result":{"sessionId":"s-1","modes":{"currentModeId":"default","availableModes":[{"id":"plan","name":"Plan"},{"id":"default","name":"Default"}]}}}`)
		case "session/prompt":
			write(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s-1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"ОК"}}}}`)
			write(`{"jsonrpc":"2.0","id":"perm","method":"session/request_permission","params":{"sessionId":"s-1","options":[{"optionId":"always","kind":"allow_always"},{"optionId":"once","kind":"allow_once"}]}}`)
		case "session/cancel":
			write(`{"jsonrpc":"2.0","id":` + string(rawID) + `,"result":{}}`)
		}
	}
}

func newPipeTransport(t *testing.T) (*Transport, net.Conn) {
	t.Helper()
	clientSide, agentSide := net.Pipe()
	go func() {
		defer agentSide.Close()
		fakeAgent(t, agentSide, bufio.NewReader(agentSide))
	}()
	return &Transport{in: clientSide, out: bufio.NewReader(clientSide), closer: clientSide}, clientSide
}

func TestClientHandshakeAndHandlers(t *testing.T) {
	transport, _ := newPipeTransport(t)
	client := NewClient(transport, ClientInfo{Name: "test", Version: "0"})
	client.Close()

	updates := make(chan string, 16)
	approvals := make(chan string, 1)
	client.SetHandlers(Handlers{
		OnUpdate: func(update Update) {
			updates <- string(update.Update)
		},
		OnPermission: func(request PermissionRequest) PermissionOutcome {
			reply := SelectAllowOnce(request.Options)
			approvals <- reply.OptionID
			return reply
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	info, err := client.Initialize(ctx)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if info.AgentInfo.Title != "GigaCode" || info.ProtocolVersion != 1 {
		t.Fatalf("unexpected initialize result: %+v", info)
	}

	session, err := client.NewSession(ctx, "/work", nil)
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if session.SessionID != "s-1" || session.Modes == nil || len(session.Modes.AvailableModes) != 2 {
		t.Fatalf("unexpected session: %+v", session)
	}

	result, err := client.Prompt(ctx, session.SessionID, "Привет")
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if result.StopReason != "end_turn" {
		t.Fatalf("unexpected stop reason: %q", result.StopReason)
	}

	select {
	case option := <-approvals:
		if option != "once" {
			t.Fatalf("expected least-privilege approval, got %q", option)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("permission request was not answered")
	}
	seen := []string{}
	for {
		select {
		case update := <-updates:
			seen = append(seen, update)
			if strings.Contains(update, "tool_call") && len(seen) >= 2 {
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("unexpected updates: %v", seen)
		}
	}
}

func TestClientSurfacesAgentErrors(t *testing.T) {
	clientSide, agentSide := net.Pipe()
	go func() {
		defer agentSide.Close()
		reader := bufio.NewReader(agentSide)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			var message map[string]any
			_ = json.Unmarshal([]byte(line), &message)
			rawID, _ := json.Marshal(message["id"])
			_, _ = agentSide.Write([]byte(`{"jsonrpc":"2.0","id":` + string(rawID) + `,"error":{"code":-32602,"message":"Invalid params"}}` + "\n"))
		}
	}()
	transport := &Transport{in: clientSide, out: bufio.NewReader(clientSide), closer: clientSide}
	client := NewClient(transport, ClientInfo{Name: "test", Version: "0"})
	client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.NewSession(ctx, "/work", nil); err == nil || !strings.Contains(err.Error(), "Invalid params") {
		t.Fatalf("expected the agent error to surface, got %v", err)
	}
}

func TestTransportSkipsNonProtocolLines(t *testing.T) {
	reader, writer := io.Pipe()
	transport := &Transport{in: nil, out: bufio.NewReader(reader)}
	go func() {
		_, _ = writer.Write([]byte("Warning: something noisy\n"))
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}` + "\n"))
		_ = writer.Close()
	}()
	message, err := transport.Read()
	if err != nil {
		t.Fatal(err)
	}
	if message["jsonrpc"] != "2.0" {
		t.Fatalf("unexpected message: %v", message)
	}
}
