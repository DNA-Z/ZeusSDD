package bridge

import (
	"encoding/json"
	"net"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestBridgeServesMCP(t *testing.T) {
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 18085}
	request := func(config Config, remote string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://"+addr.String()+"/mcp",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		r.RemoteAddr = remote
		w := httptest.NewRecorder()
		bridgeRoutes(config, addr, "http://127.0.0.1:8081").ServeHTTP(w, r)
		return w
	}

	w := request(Config{}, "127.0.0.1:12345")
	var response struct {
		Result struct {
			Tools []struct{ Name string }
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); w.Code != 200 || err != nil {
		t.Fatalf("MCP tools/list: %d %s", w.Code, w.Body)
	}
	var names []string
	for _, tool := range response.Result.Tools {
		names = append(names, tool.Name)
	}
	if !slices.Equal(names, []string{"list_tasks", "get_task", "list_messages", "post_message"}) {
		t.Fatalf("unexpected tools: %v", names)
	}

	if w := request(Config{}, "192.0.2.1:12345"); w.Code != 403 {
		t.Fatalf("non-local request: %d", w.Code)
	}
	if w := request(Config{MCPURL: "http://example.test/mcp"}, "127.0.0.1:12345"); w.Code != 404 {
		t.Fatalf("MCP override left embedded endpoint enabled: %d", w.Code)
	}
}
