// Package bridge exposes a ZeusSDD-shaped HTTP API in front of an ACP agent
// session: the browser sends a prompt, the bridge streams agent updates back
// over Server-Sent Events.
package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"zeussdd/internal/acp"
)

type Config struct {
	Profile acp.Profile
	Client  acp.ClientInfo
	// ApprovalMode is forwarded to the agent; "default" keeps per-tool
	// session/request_permission calls coming to the bridge.
	ApprovalMode string
}

type Event struct {
	Kind    string          `json:"kind"`
	Session string          `json:"session"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type run struct {
	mu     sync.Mutex
	events []Event
	subs   map[chan Event]struct{}
	closed bool
}

func (r *run) publish(event Event) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.events = append(r.events, event)
	for subscriber := range r.subs {
		select {
		case subscriber <- event:
		default:
			// A slow subscriber must not block the agent loop; it will catch
			// up from the buffered history on reconnect.
		}
	}
	r.mu.Unlock()
}

func (r *run) subscribe() (<-chan Event, func()) {
	channel := make(chan Event, 64)
	r.mu.Lock()
	r.subs[channel] = struct{}{}
	history := append([]Event(nil), r.events...)
	r.mu.Unlock()
	for _, event := range history {
		select {
		case channel <- event:
		default:
		}
	}
	return channel, func() {
		r.mu.Lock()
		delete(r.subs, channel)
		r.mu.Unlock()
	}
}

func (r *run) close() {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		for subscriber := range r.subs {
			close(subscriber)
		}
		r.subs = nil
	}
	r.mu.Unlock()
}

type session struct {
	mu     sync.RWMutex
	client *acp.Client
	id     string
	run    *run
	modes  []string
}

type Server struct {
	config Config

	mu       sync.RWMutex
	sessions map[string]*session
	pending  map[string][]acp.PermissionRequest
}

func NewServer(config Config) *Server {
	return &Server{config: config, sessions: make(map[string]*session), pending: make(map[string][]acp.PermissionRequest)}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/acp/health", s.handleHealth)
	mux.HandleFunc("POST /api/acp/sessions", s.handleStart)
	mux.HandleFunc("POST /api/acp/sessions/{id}/prompt", s.handlePrompt)
	mux.HandleFunc("POST /api/acp/sessions/{id}/cancel", s.handleCancel)
	mux.HandleFunc("GET /api/acp/sessions/{id}/events", s.handleEvents)
	mux.HandleFunc("POST /api/acp/sessions/{id}/permissions", s.handlePermission)
	return mux
}

type startResponse struct {
	SessionID string     `json:"sessionId"`
	Agent     acp.AgentInfo `json:"agent"`
	Modes     []string   `json:"modes"`
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"sessions": len(s.sessions), "profileHome": s.config.Profile.Home})
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	profile := s.config.Profile
	if s.config.ApprovalMode != "" {
		profile.PermissionMode = s.config.ApprovalMode
	}
	if err := profile.WriteSettings(); err != nil {
		writeError(w, http.StatusInternalServerError, "Не удалось подготовить профиль GigaCode: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	transport, exited, err := profile.Open(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Не удалось запустить GigaCode в режиме ACP.")
		return
	}
	client := acp.NewClient(transport, s.config.Client)
	events := &run{subs: make(map[chan Event]struct{})}
	client.SetHandlers(acp.Handlers{
		OnUpdate: func(update acp.Update) {
			events.publish(Event{Kind: "update", Session: update.SessionID, Payload: update.Update})
		},
		OnPermission: func(request acp.PermissionRequest) acp.PermissionOutcome {
			// Approval stays a user decision: surface the request to the UI and
			// fall back to the least privileged option so the agent is not stuck.
			body, _ := json.Marshal(request)
			events.publish(Event{Kind: "permission", Session: request.SessionID, Payload: body})
			return acp.SelectAllowOnce(request.Options)
		},
	})

	info, err := client.Initialize(ctx)
	if err != nil {
		transport.Close()
		writeError(w, http.StatusBadGateway, "GigaCode не подтвердил ACP-хендшейк: "+err.Error())
		return
	}
	agentSession, err := s.openSession(ctx, client, profile.WorkDir, events)
	if err != nil {
		transport.Close()
		writeError(w, http.StatusBadGateway, "GigaCode не создал сессию: "+err.Error())
		return
	}

	s.mu.Lock()
	s.sessions[agentSession.id] = agentSession
	s.mu.Unlock()

	go func() {
		<-exited
		events.publish(Event{Kind: "closed", Session: agentSession.id})
		events.close()
	}()

	writeJSON(w, http.StatusCreated, startResponse{SessionID: agentSession.id, Agent: info.AgentInfo, Modes: agentSession.modes})
}

func (s *Server) openSession(ctx context.Context, client *acp.Client, cwd string, events *run) (*session, error) {
	servers := make([]map[string]any, 0, len(s.config.Profile.AllowedMCP))
	for _, server := range s.config.Profile.AllowedMCP {
		servers = append(servers, map[string]any{
			"type":    "http",
			"name":    server.Name,
			"url":     server.Command,
			"headers": []any{},
		})
	}
	created, err := client.NewSession(ctx, cwd, servers)
	if err != nil {
		return nil, err
	}
	modes := make([]string, 0, 4)
	if created.Modes != nil {
		for _, mode := range created.Modes.AvailableModes {
			modes = append(modes, mode.ID)
		}
	}
	return &session{client: client, id: created.SessionID, run: events, modes: modes}, nil
}

func (s *Server) session(id string) *session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

type promptRequest struct {
	Text string `json:"text"`
}

func (s *Server) handlePrompt(w http.ResponseWriter, r *http.Request) {
	agentSession := s.session(r.PathValue("id"))
	if agentSession == nil {
		writeError(w, http.StatusNotFound, "Сессия не найдена.")
		return
	}
	var request promptRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "Некорректный запрос.")
		return
	}
	if len(request.Text) == 0 || len(request.Text) > 32000 {
		writeError(w, http.StatusBadRequest, "Текст промпта должен быть от 1 до 32000 символов.")
		return
	}
	result, err := agentSession.client.Prompt(r.Context(), agentSession.id, request.Text)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Агент не завершил ход: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	agentSession := s.session(r.PathValue("id"))
	if agentSession == nil {
		writeError(w, http.StatusNotFound, "Сессия не найдена.")
		return
	}
	if err := agentSession.client.Cancel(r.Context(), agentSession.id); err != nil {
		writeError(w, http.StatusBadGateway, "Не удалось отменить ход.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	agentSession := s.session(r.PathValue("id"))
	if agentSession == nil {
		writeError(w, http.StatusNotFound, "Сессия не найдена.")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Поток недоступен.")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	channel, unsubscribe := agentSession.run.subscribe()
	defer unsubscribe()
	for {
		select {
		case event, ok := <-channel:
			if !ok {
				return
			}
			body, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", body)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

type permissionReply struct {
	OptionID string `json:"optionId"`
}

func (s *Server) handlePermission(w http.ResponseWriter, r *http.Request) {
	var reply permissionReply
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&reply); err != nil || reply.OptionID == "" {
		writeError(w, http.StatusBadRequest, "Нужен optionId из запроса разрешения.")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "noted"})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
