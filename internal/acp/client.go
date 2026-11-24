package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

type ClientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

type AgentInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Version string `json:"version"`
}

type InitializeResult struct {
	ProtocolVersion   int       `json:"protocolVersion"`
	AgentInfo         AgentInfo `json:"agentInfo"`
	AuthMethods       []any     `json:"authMethods"`
	AgentCapabilities struct {
		MCP struct {
			HTTP bool `json:"http"`
		} `json:"mcpCapabilities"`
	} `json:"agentCapabilities"`
}

type Session struct {
	SessionID string `json:"sessionId"`
	Modes     *struct {
		CurrentModeID  string `json:"currentModeId"`
		AvailableModes []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"availableModes"`
	} `json:"modes"`
}

type PromptResult struct {
	StopReason string `json:"stopReason"`
}

type Update struct {
	SessionID string          `json:"sessionId"`
	Update    json.RawMessage `json:"update"`
}

type PermissionRequest struct {
	SessionID string             `json:"sessionId"`
	ToolCall  json.RawMessage    `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

type PermissionOption struct {
	ID   string `json:"optionId"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type PermissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

// Client speaks ACP over a transport. Requests and notifications from the agent
// are demultiplexed in a single read loop, so handlers must not block it.
type Client struct {
	transport *Transport
	client    ClientInfo

	writeMu  sync.Mutex
	nextID   int
	pending  map[int]chan rpcResult
	readErr  error
	mu       sync.Mutex
	handlers Handlers
	done     chan struct{}
}

type Handlers struct {
	// OnUpdate receives every session/update notification.
	OnUpdate func(Update)
	// OnPermission runs independently of the reader and may wait for the user.
	OnPermission func(PermissionRequest) PermissionOutcome
	// OnClientRequest handles other agent-to-client requests (fs/*, terminal/*).
	// Returning a nil value and a non-nil error sends a JSON-RPC error back.
	OnClientRequest func(method string, params json.RawMessage) (any, error)
}

type rpcResult struct {
	result json.RawMessage
	err    *rpcErrorBody
}

func (r rpcResult) response(method string) (json.RawMessage, error) {
	if r.err != nil {
		return nil, fmt.Errorf("acp: %s: %w", method, r.err)
	}
	return r.result, nil
}

type rpcErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcErrorBody) Error() string { return fmt.Sprintf("%d: %s", e.Code, e.Message) }

func IsAuthRequired(err error) bool {
	var failure *rpcErrorBody
	return errors.As(err, &failure) && failure.Code == -32000
}

func NewClient(transport *Transport, info ClientInfo) *Client {
	client := &Client{
		transport: transport,
		client:    info,
		pending:   make(map[int]chan rpcResult),
		done:      make(chan struct{}),
	}
	go client.readLoop()
	return client
}

func (c *Client) SetHandlers(handlers Handlers) {
	c.mu.Lock()
	c.handlers = handlers
	c.mu.Unlock()
}

func (c *Client) Done() <-chan struct{} { return c.done }

// Close releases the transport. Pending requests fail through the read loop
// when the agent side goes away.
func (c *Client) Close() error {
	return c.transport.Close()
}

func (c *Client) readLoop() {
	defer close(c.done)
	for {
		message, err := c.transport.Read()
		if err != nil {
			c.fail(fmt.Errorf("acp: connection closed: %w", err))
			return
		}
		c.dispatch(message)
	}
}

func (c *Client) fail(err error) {
	c.writeMu.Lock()
	pending := c.pending
	c.pending = make(map[int]chan rpcResult)
	c.readErr = err
	c.writeMu.Unlock()
	for _, waiter := range pending {
		waiter <- rpcResult{err: &rpcErrorBody{Code: -32603, Message: err.Error()}}
	}
}

func (c *Client) dispatch(message map[string]any) {
	method, _ := message["method"].(string)
	id, hasID := message["id"]
	params, _ := json.Marshal(message["params"])

	c.mu.Lock()
	handlers := c.handlers
	c.mu.Unlock()

	if method == "" {
		// Response to one of our requests.
		number, ok := toInt(id)
		if !ok {
			return
		}
		waiter := c.takePending(number)
		if waiter == nil {
			return
		}
		if raw, ok := message["error"].(map[string]any); ok {
			code := 0
			if value, ok := toInt(raw["code"]); ok {
				code = value
			}
			text, _ := raw["message"].(string)
			waiter <- rpcResult{err: &rpcErrorBody{Code: code, Message: text}}
			return
		}
		result, _ := json.Marshal(message["result"])
		waiter <- rpcResult{result: result}
		return
	}

	if !hasID {
		if method == "session/update" && handlers.OnUpdate != nil {
			var update Update
			if json.Unmarshal(params, &update) == nil {
				handlers.OnUpdate(update)
			}
		}
		return
	}

	switch {
	case method == "session/request_permission" && handlers.OnPermission != nil:
		var request PermissionRequest
		if err := json.Unmarshal(params, &request); err != nil {
			c.replyError(id, -32602, "invalid permission request")
			return
		}
		go func() {
			c.reply(id, map[string]any{"outcome": handlers.OnPermission(request)})
		}()
	case handlers.OnClientRequest != nil:
		go func() {
			result, err := handlers.OnClientRequest(method, params)
			if err != nil {
				c.replyError(id, -32601, err.Error())
				return
			}
			c.reply(id, result)
		}()
	default:
		c.replyError(id, -32601, "client does not implement "+method)
	}
}

func (c *Client) takePending(id int) chan rpcResult {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	waiter, ok := c.pending[id]
	if !ok {
		return nil
	}
	delete(c.pending, id)
	return waiter
}

func (c *Client) request(ctx context.Context, method string, params any, sent func()) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.writeMu.Lock()
	if c.readErr != nil {
		err := c.readErr
		c.writeMu.Unlock()
		return nil, err
	}
	c.nextID++
	id := c.nextID
	waiter := make(chan rpcResult, 1)
	c.pending[id] = waiter
	c.writeMu.Unlock()

	if err := c.transport.Write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		c.takePending(id)
		return nil, err
	}
	if sent != nil {
		sent()
	}
	select {
	case result := <-waiter:
		return result.response(method)
	case <-ctx.Done():
		c.takePending(id)
		select {
		case result := <-waiter:
			return result.response(method)
		default:
			return nil, ctx.Err()
		}
	}
}

func (c *Client) reply(id any, result any) {
	if result == nil {
		result = map[string]any{}
	}
	_ = c.transport.Write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (c *Client) replyError(id any, code int, message string) {
	_ = c.transport.Write(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}

func (c *Client) Initialize(ctx context.Context) (InitializeResult, error) {
	raw, err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs":       map[string]bool{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
		"clientInfo": c.client,
	}, nil)
	if err != nil {
		return InitializeResult{}, err
	}
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return InitializeResult{}, err
	}
	if result.ProtocolVersion != 1 {
		return InitializeResult{}, fmt.Errorf("acp: unsupported protocol version %d", result.ProtocolVersion)
	}
	return result, nil
}

func (c *Client) NewSession(ctx context.Context, cwd string, mcpServers []map[string]any) (Session, error) {
	if mcpServers == nil {
		mcpServers = []map[string]any{}
	}
	raw, err := c.request(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": mcpServers}, nil)
	if err != nil {
		return Session{}, err
	}
	var session Session
	if err := json.Unmarshal(raw, &session); err != nil {
		return Session{}, err
	}
	if session.SessionID == "" {
		return Session{}, errors.New("acp: agent returned no session id")
	}
	return session, nil
}

func (c *Client) Prompt(ctx context.Context, sessionID, text string, sent func()) (PromptResult, error) {
	raw, err := c.request(ctx, "session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": text}},
	}, sent)
	if err != nil {
		return PromptResult{}, err
	}
	var result PromptResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return PromptResult{}, err
	}
	if result.StopReason == "" {
		return PromptResult{}, errors.New("acp: agent returned no stop reason")
	}
	return result, nil
}

func (c *Client) Cancel(sessionID string) error {
	return c.transport.Write(map[string]any{
		"jsonrpc": "2.0",
		"method":  "session/cancel",
		"params":  map[string]any{"sessionId": sessionID},
	})
}

func toInt(value any) (int, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	integer, err := number.Int64()
	return int(integer), err == nil && int64(int(integer)) == integer
}
