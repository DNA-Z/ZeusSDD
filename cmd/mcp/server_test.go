package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func call(t *testing.T, handler http.Handler, body string) (int, map[string]any) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body)))
	var value map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	return recorder.Code, value
}

func TestInitializeAndListTools(t *testing.T) {
	handler := newServer("http://127.0.0.1:1")
	code, response := call(t, handler, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if code != http.StatusOK || response["error"] != nil {
		t.Fatalf("initialize failed: %d %v", code, response)
	}
	result := response["result"].(map[string]any)
	if result["protocolVersion"] == nil || result["capabilities"] == nil || result["serverInfo"] == nil {
		t.Fatalf("incomplete initialize result: %v", result)
	}

	_, response = call(t, handler, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tools := response["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(tools))
	}
	for _, item := range tools {
		value := item.(map[string]any)
		if value["name"] == nil || value["description"] == nil || value["inputSchema"] == nil {
			t.Fatalf("incomplete tool definition: %v", value)
		}
	}
}

func TestNotificationIsAccepted(t *testing.T) {
	handler := newServer("http://127.0.0.1:1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for notification, got %d", recorder.Code)
	}
}

func TestUnknownMethod(t *testing.T) {
	handler := newServer("http://127.0.0.1:1")
	_, response := call(t, handler, `{"jsonrpc":"2.0","id":3,"method":"resources/list"}`)
	failure, ok := response["error"].(map[string]any)
	if !ok || failure["code"].(float64) != -32601 {
		t.Fatalf("expected method not found, got %v", response)
	}
}

func TestToolsReadWorkflow(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/workspace":
			_, _ = w.Write([]byte(`{"releases":[{"id":"2026.1128","tasks":[{"key":"ZEUS-214","title":"Сверка","status":"progress","branch":"feature/ZEUS-214","files":[{},{}]}]}]}`))
		case "/api/tasks/ZEUS-214":
			_, _ = w.Write([]byte(`{"task":{"key":"ZEUS-214","title":"Сверка","status":"progress","branch":"feature/ZEUS-214","summary":"Подключить сверку.","files":[{"path":"spec.md","lines":[["add","+# Сверка"]]}]},"release":{"id":"2026.1128"}}`))
		case "/api/tasks/ZEUS-214/messages":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":7,"text":"привет","createdAt":"2026-11-23T10:00:00Z"}`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":1,"text":"старое","createdAt":"2026-11-23T09:00:00Z"}]`))
		case "/api/tasks/ZEUS-999":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Задача не найдена."}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	handler := newServer(upstream.URL)

	_, response := call(t, handler, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"list_tasks","arguments":{}}}`)
	if got := toolText(t, response); !strings.Contains(got, "ZEUS-214") || !strings.Contains(got, "в работе") {
		t.Fatalf("unexpected list_tasks output: %q", got)
	}

	_, response = call(t, handler, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get_task","arguments":{"key":"ZEUS-214"}}}`)
	if got := toolText(t, response); !strings.Contains(got, "+ # Сверка") || !strings.Contains(got, "spec.md") {
		t.Fatalf("unexpected get_task output: %q", got)
	}

	_, response = call(t, handler, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"list_messages","arguments":{"key":"ZEUS-214"}}}`)
	if got := toolText(t, response); !strings.Contains(got, "старое") {
		t.Fatalf("unexpected list_messages output: %q", got)
	}

	_, response = call(t, handler, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"post_message","arguments":{"key":"ZEUS-214","text":"привет"}}}`)
	if got := toolText(t, response); !strings.Contains(got, "#7") {
		t.Fatalf("unexpected post_message output: %q", got)
	}

	_, response = call(t, handler, `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"get_task","arguments":{"key":"ZEUS-999"}}}`)
	result := response["result"].(map[string]any)
	if result["isError"] != true || !strings.Contains(toolText(t, response), "Задача не найдена") {
		t.Fatalf("expected tool error for missing task: %v", result)
	}
}

func TestToolsRejectBadInput(t *testing.T) {
	cases := []string{
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"get_task","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"get_task","arguments":{"key":"../etc/passwd"}}}`,
		`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"unknown","arguments":{"key":"ZEUS-214"}}}`,
	}
	for _, body := range cases {
		_, response := call(t, newServer("http://127.0.0.1:1"), body)
		if _, ok := response["error"].(map[string]any); !ok {
			if result, ok := response["result"].(map[string]any); !ok || result["isError"] != true {
				t.Fatalf("expected rejection for %s, got %v", body, response)
			}
		}
	}
}

func TestWorkflowUnavailable(t *testing.T) {
	_, response := call(t, newServer("http://127.0.0.1:1"), `{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"list_tasks","arguments":{}}}`)
	result := response["result"].(map[string]any)
	if result["isError"] != true || !strings.Contains(toolText(t, response), "недоступен") {
		t.Fatalf("expected unavailable error, got %v", result)
	}
}

func TestGETRejected(t *testing.T) {
	recorder := httptest.NewRecorder()
	newServer("http://127.0.0.1:1").ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET, got %d", recorder.Code)
	}
}

func toolText(t *testing.T, response map[string]any) string {
	t.Helper()
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing result: %v", response)
	}
	items, ok := result["content"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("missing content: %v", result)
	}
	return items[0].(map[string]any)["text"].(string)
}
