package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type server struct {
	workflow *http.Client
	base     string
}

const protocolVersion = "2025-06-18"

func newServer(base string) *server {
	return &server{
		workflow: &http.Client{Timeout: 10 * time.Second},
		base:     strings.TrimRight(base, "/"),
	}
}

func tools() []tool {
	return []tool{
		{
			Name:        "list_tasks",
			Description: "Список задач рабочего пространства ZeusSDD с релизом, статусом, веткой и числом изменённых файлов.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "get_task",
			Description: "Полное описание задачи ZeusSDD: сводка, роли, зависимости, связи и диффы изменённых файлов.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"key": map[string]any{"type": "string", "description": "Ключ задачи, например ZEUS-214"}},
				"required":   []string{"key"},
			},
		},
		{
			Name:        "list_messages",
			Description: "Сообщения чата по задаче ZeusSDD в порядке отправки.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"key": map[string]any{"type": "string", "description": "Ключ задачи, например ZEUS-214"}},
				"required":   []string{"key"},
			},
		},
		{
			Name:        "post_message",
			Description: "Отправить сообщение в чат по задаче ZeusSDD от имени пользователя.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key":  map[string]any{"type": "string", "description": "Ключ задачи, например ZEUS-214"},
					"text": map[string]any{"type": "string", "description": "Текст сообщения, от 1 до 4000 символов"},
				},
				"required": []string{"key", "text"},
			},
		},
	}
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	case http.MethodPost:
	default:
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var request rpcRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(&request); err != nil {
		writeRPC(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "Parse error"}})
		return
	}
	if request.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	result, failure := s.dispatch(r.Context(), request)
	response := rpcResponse{JSONRPC: "2.0", ID: request.ID}
	if failure != nil {
		response.Error = failure
	} else {
		response.Result = result
	}
	writeRPC(w, http.StatusOK, response)
}

func (s *server) dispatch(ctx context.Context, request rpcRequest) (any, *rpcError) {
	switch request.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "zeussdd", "version": "0.1.0"},
			"instructions":    "ZeusSDD — рабочее пространство релизов и задач команды. Инструменты читают демо-данные через HTTP API сервиса Workflow.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": tools()}, nil
	case "tools/call":
		return s.callTool(ctx, request.Params)
	default:
		return nil, &rpcError{Code: -32601, Message: "Method not found"}
	}
}

func (s *server) callTool(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &call); err != nil {
		return nil, &rpcError{Code: -32602, Message: "Invalid params"}
	}
	var arguments struct {
		Key  string `json:"key"`
		Text string `json:"text"`
	}
	if len(call.Arguments) > 0 {
		if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
			return nil, &rpcError{Code: -32602, Message: "Invalid params"}
		}
	}
	key := strings.TrimSpace(arguments.Key)
	if call.Name != "list_tasks" && key == "" {
		return toolError("Укажите ключ задачи."), nil
	}
	if key != "" && !validKey(key) {
		return toolError("Ключ задачи должен выглядеть как ZEUS-214."), nil
	}
	switch call.Name {
	case "list_tasks":
		tasks, err := s.listTasks(ctx)
		if err != nil {
			return toolError(err.Error()), nil
		}
		return text(tasks), nil
	case "get_task":
		detail, err := s.getTask(ctx, key)
		if err != nil {
			return toolError(err.Error()), nil
		}
		return text(detail), nil
	case "list_messages":
		messages, err := s.listMessages(ctx, key)
		if err != nil {
			return toolError(err.Error()), nil
		}
		if len(messages) == 0 {
			return text("Сообщений пока нет."), nil
		}
		var builder strings.Builder
		for _, item := range messages {
			fmt.Fprintf(&builder, "%s · #%d\n%s\n\n", item.CreatedAt.Format(time.RFC3339), item.ID, item.Text)
		}
		return text(builder.String()), nil
	case "post_message":
		message, err := s.postMessage(ctx, key, arguments.Text)
		if err != nil {
			return toolError(err.Error()), nil
		}
		return text(fmt.Sprintf("Сообщение #%d отправлено.", message.ID)), nil
	default:
		return nil, &rpcError{Code: -32602, Message: "Unknown tool"}
	}
}

type taskSummary struct {
	Key       string `json:"key"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Kind      string `json:"kind"`
	Branch    string `json:"branch"`
	Developer string `json:"developer"`
	Release   string `json:"release"`
	Files     int    `json:"files"`
}

func (s *server) listTasks(ctx context.Context) (string, error) {
	var state struct {
		Releases []struct {
			ID    string `json:"id"`
			Tasks []struct {
				Key       string `json:"key"`
				Title     string `json:"title"`
				Status    string `json:"status"`
				Kind      string `json:"kind"`
				Branch    string `json:"branch"`
				Developer string `json:"developer"`
				Files     []any  `json:"files"`
			} `json:"tasks"`
		} `json:"releases"`
	}
	if err := s.get(ctx, "/api/workspace", &state); err != nil {
		return "", err
	}
	var builder strings.Builder
	for _, release := range state.Releases {
		for _, task := range release.Tasks {
			fmt.Fprintf(&builder, "%s · %s · %s · %s · %s · файлов: %d\n",
				task.Key, task.Title, statusName(task.Status), release.ID, task.Branch, len(task.Files))
		}
	}
	return builder.String(), nil
}

func statusName(status string) string {
	switch status {
	case "todo":
		return "не взята в работу"
	case "progress":
		return "в работе"
	case "testing":
		return "на тестировании"
	default:
		return status
	}
}

func (s *server) getTask(ctx context.Context, key string) (string, error) {
	var detail struct {
		Task struct {
			Key          string `json:"key"`
			Title        string `json:"title"`
			Status       string `json:"status"`
			Kind         string `json:"kind"`
			Branch       string `json:"branch"`
			Developer    string `json:"developer"`
			Analyst      string `json:"analyst"`
			Tester       string `json:"tester"`
			Summary      string `json:"summary"`
			Dependencies []struct {
				Key       string `json:"key"`
				Condition string `json:"condition"`
			} `json:"dependencies"`
			Files []struct {
				Path  string      `json:"path"`
				Lines [][2]string `json:"lines"`
			} `json:"files"`
		} `json:"task"`
		Release struct {
			ID string `json:"id"`
		} `json:"release"`
	}
	if err := s.get(ctx, "/api/tasks/"+url.PathEscape(key), &detail); err != nil {
		return "", err
	}
	task := detail.Task
	var builder strings.Builder
	fmt.Fprintf(&builder, "%s — %s\nРелиз: %s · статус: %s · тип: %s\nВетка: %s\nРазработчик: %s · аналитик: %s", task.Key, task.Title, detail.Release.ID, statusName(task.Status), task.Kind, task.Branch, task.Developer, task.Analyst)
	if task.Tester != "" {
		fmt.Fprintf(&builder, " · тестировщик: %s", task.Tester)
	}
	fmt.Fprintf(&builder, "\n\n%s\n", task.Summary)
	for _, dependency := range task.Dependencies {
		fmt.Fprintf(&builder, "\nЗависимость %s: %s", dependency.Key, dependency.Condition)
	}
	if len(task.Files) == 0 {
		builder.WriteString("\nИзменённых файлов нет.")
	}
	for _, file := range task.Files {
		fmt.Fprintf(&builder, "\n\n### %s\n", file.Path)
		for _, line := range file.Lines {
			kind, text := line[0], line[1]
			switch kind {
			case "add":
				fmt.Fprintf(&builder, "+ %s\n", text)
			case "remove":
				fmt.Fprintf(&builder, "- %s\n", text)
			default:
				fmt.Fprintf(&builder, "  %s\n", text)
			}
		}
	}
	return builder.String(), nil
}

type chatMessage struct {
	ID        int64     `json:"id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *server) listMessages(ctx context.Context, key string) ([]chatMessage, error) {
	var items []chatMessage
	if err := s.get(ctx, "/api/tasks/"+url.PathEscape(key)+"/messages", &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *server) postMessage(ctx context.Context, key, value string) (chatMessage, error) {
	body, err := json.Marshal(map[string]string{"text": value})
	if err != nil {
		return chatMessage{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/api/tasks/"+url.PathEscape(key)+"/messages", bytes.NewReader(body))
	if err != nil {
		return chatMessage{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	var message chatMessage
	if err := s.do(request, &message); err != nil {
		return chatMessage{}, err
	}
	return message, nil
}

func (s *server) get(ctx context.Context, path string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+path, nil)
	if err != nil {
		return err
	}
	return s.do(request, target)
}

func (s *server) do(request *http.Request, target any) error {
	response, err := s.workflow.Do(request)
	if err != nil {
		return fmt.Errorf("Сервис задач недоступен.")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		var failure struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 4<<10)).Decode(&failure) == nil && failure.Error != "" {
			return fmt.Errorf("%s", failure.Error)
		}
		return fmt.Errorf("Сервис задач ответил кодом %d.", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return fmt.Errorf("Сервис задач вернул неожиданный ответ.")
	}
	return nil
}

func validKey(key string) bool {
	if len(key) < 5 || len(key) > 40 {
		return false
	}
	for i, r := range key {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' && i > 0:
		default:
			return false
		}
	}
	return true
}

func text(value string) map[string]any {
	return map[string]any{"content": []content{{Type: "text", Text: value}}}
}

func toolError(message string) map[string]any {
	return map[string]any{"content": []content{{Type: "text", Text: message}}, "isError": true}
}

func writeRPC(w http.ResponseWriter, status int, value rpcResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
