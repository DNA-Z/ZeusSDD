// Package bridge connects the task chat to a local ACP subprocess.
package bridge

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"zeussdd/internal/acp"
	"zeussdd/internal/localhttp"
	"zeussdd/internal/workspace"
)

const authMessage = "Войдите в GigaCode CLI с тем же профилем ACP_HOME и повторите попытку."

type Config struct {
	Profile acp.Profile
	Client  acp.ClientInfo
	MCPURL  string
}

type Server struct {
	config Config

	prepareOnce sync.Once
	prepareErr  error
	mu          sync.RWMutex
	sessions    map[string]*session
	recent      map[string]*journal
}

func NewServer(config Config) *Server {
	return &Server{config: config, sessions: make(map[string]*session), recent: make(map[string]*journal)}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/acp/health", s.handleHealth)
	mux.HandleFunc("POST /api/acp/sessions", s.handleStart)
	mux.HandleFunc("POST /api/acp/sessions/{id}/prompt", s.handlePrompt)
	mux.HandleFunc("POST /api/acp/sessions/{id}/cancel", s.handleCancel)
	mux.HandleFunc("GET /api/acp/sessions/{id}/events", s.handleEvents)
	mux.HandleFunc("POST /api/acp/sessions/{id}/permissions", s.handlePermission)
	mux.HandleFunc("DELETE /api/acp/sessions/{id}", s.handleClose)
	return localhttp.Only(mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"sessions": len(s.sessions), "profileHome": s.config.Profile.Home})
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	var request struct{ TaskKey string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "Некорректный запрос.")
		return
	}
	var taskContext string
	if request.TaskKey != "" {
		state, err := workspace.Load()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Не удалось загрузить задачу.")
			return
		}
		task, _, ok := state.Task(request.TaskKey)
		if !ok {
			writeError(w, http.StatusNotFound, "Задача не найдена.")
			return
		}
		taskContext = fmt.Sprintf("Контекст задачи ZeusSDD %s\nНазвание: %s\nОписание: %s\nВетка: %s\n\nЗапрос пользователя:\n", task.Key, task.Title, task.Summary, task.Branch)
	}
	s.prepareOnce.Do(func() { s.prepareErr = s.config.Profile.Prepare() })
	if s.prepareErr != nil {
		writeError(w, http.StatusInternalServerError, "Не удалось подготовить профиль GigaCode: "+s.prepareErr.Error())
		return
	}

	setup, cancelSetup := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancelSetup()
	lifetime, stop := context.WithCancel(context.Background())
	transport, exited, err := s.config.Profile.Open(lifetime)
	if err != nil {
		stop()
		writeError(w, http.StatusBadGateway, "Не удалось запустить GigaCode. Проверьте PATH или ZEUSSDD_GIGACODE_PATH. "+err.Error())
		return
	}
	client := acp.NewClient(transport, s.config.Client)
	active := &session{
		id: rand.Text(), taskContext: taskContext, ctx: lifetime, stop: stop, client: client,
		events: journal{changed: make(chan struct{})}, pending: make(map[string]*permission),
	}
	client.SetHandlers(acp.Handlers{
		OnUpdate: func(update acp.Update) {
			active.publish("update", update.Update)
		},
		OnPermission: active.onPermission,
	})

	info, err := client.Initialize(setup)
	var created acp.Session
	if err == nil {
		var servers []map[string]any
		if s.config.MCPURL != "" {
			if !info.AgentCapabilities.MCP.HTTP {
				err = errors.New("этот агент не объявил поддержку MCP по HTTP")
			} else {
				servers = []map[string]any{{
					"type": "http", "name": "zeussdd", "url": s.config.MCPURL, "headers": []any{},
				}}
			}
		}
		if err == nil {
			created, err = client.NewSession(setup, s.config.Profile.WorkDir, servers)
		}
	}
	if err == nil {
		err = setup.Err()
	}
	if err != nil {
		stop()
		_ = client.Close()
		if acp.IsAuthRequired(err) {
			writeError(w, http.StatusUnauthorized, authMessage)
			return
		}
		writeError(w, http.StatusBadGateway, "GigaCode не открыл сессию ACP: "+err.Error())
		return
	}
	active.mu.Lock()
	active.agentID = created.SessionID
	active.mu.Unlock()

	s.mu.Lock()
	s.sessions[active.id] = active
	s.mu.Unlock()

	go func() {
		select {
		case <-client.Done():
			stop()
		case <-lifetime.Done():
		}
	}()
	go func() {
		exitErr := <-exited
		drain := time.NewTimer(2 * time.Second)
		select {
		case <-client.Done():
		case <-lifetime.Done():
		case <-drain.C:
		}
		drain.Stop()
		stop()
		_ = client.Close()
		active.mu.Lock()
		lastTurn := active.turn
		active.mu.Unlock()
		if lastTurn != nil {
			<-lastTurn.done
		}
		active.publish("closed", nil)
		s.mu.Lock()
		delete(s.sessions, active.id)
		s.recent[active.id] = &active.events
		s.mu.Unlock()
		time.AfterFunc(2*time.Minute, func() {
			s.mu.Lock()
			delete(s.recent, active.id)
			s.mu.Unlock()
		})
		if exitErr != nil && !errors.Is(exitErr, context.Canceled) {
			log.Printf("ACP agent %s: %v", active.id, exitErr)
		}
	}()

	modes := make([]string, 0)
	if created.Modes != nil {
		for _, mode := range created.Modes.AvailableModes {
			modes = append(modes, mode.ID)
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"sessionId": active.id, "agent": info.AgentInfo, "modes": modes})
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) *session {
	s.mu.RLock()
	active := s.sessions[r.PathValue("id")]
	s.mu.RUnlock()
	if active == nil {
		writeError(w, http.StatusNotFound, "Сессия GigaCode закрыта или не найдена.")
	}
	return active
}

func (s *Server) handlePrompt(w http.ResponseWriter, r *http.Request) {
	active := s.getSession(w, r)
	if active == nil {
		return
	}
	var request struct{ Text string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "Некорректный запрос.")
		return
	}
	if strings.TrimSpace(request.Text) == "" || utf8.RuneCountInString(request.Text) > 32000 {
		writeError(w, http.StatusBadRequest, "Текст промпта должен быть от 1 до 32000 символов.")
		return
	}
	active.mu.Lock()
	if active.ctx.Err() != nil {
		active.mu.Unlock()
		writeError(w, http.StatusNotFound, "Сессия GigaCode закрыта.")
		return
	}
	if active.turn != nil {
		active.mu.Unlock()
		writeError(w, http.StatusConflict, "GigaCode уже отвечает в этой сессии.")
		return
	}
	current := &turn{sent: make(chan struct{}), done: make(chan struct{})}
	active.turn = current
	text := active.taskContext + request.Text
	active.publish("turn_started", map[string]string{"text": request.Text})
	active.mu.Unlock()

	result, err := active.client.Prompt(active.ctx, active.agentID, text, func() { close(current.sent) })
	active.mu.Lock()
	active.cancelPermissions()
	active.turn = nil
	close(current.done)
	if err != nil {
		detail := "Агент не завершил ход: " + err.Error()
		status := http.StatusBadGateway
		if acp.IsAuthRequired(err) {
			status, detail = http.StatusUnauthorized, authMessage
		}
		active.publish("turn_finished", map[string]string{"error": detail})
		active.mu.Unlock()
		writeError(w, status, detail)
		return
	}
	active.taskContext = ""
	active.publish("turn_finished", result)
	active.mu.Unlock()
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	active := s.getSession(w, r)
	if active == nil {
		return
	}
	active.mu.Lock()
	current := active.turn
	active.mu.Unlock()
	if current == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	select {
	case <-current.sent:
	case <-current.done:
		w.WriteHeader(http.StatusNoContent)
		return
	case <-r.Context().Done():
		return
	}
	active.mu.Lock()
	if active.turn != current || current.cancelling {
		active.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	current.cancelling = true
	err := active.client.Cancel(active.agentID)
	active.cancelPermissions()
	active.mu.Unlock()
	if err != nil {
		active.stop()
		writeError(w, http.StatusBadGateway, "Не удалось отменить ход.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleClose(w http.ResponseWriter, r *http.Request) {
	if active := s.getSession(w, r); active != nil {
		active.stop()
		_ = active.client.Close()
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.RLock()
	events := s.recent[id]
	if active := s.sessions[id]; active != nil {
		events = &active.events
	}
	s.mu.RUnlock()
	if events == nil {
		writeError(w, http.StatusNotFound, "Сессия GigaCode закрыта или не найдена.")
		return
	}
	cursor := 0
	if header := r.Header.Get("Last-Event-ID"); header != "" {
		var err error
		cursor, err = strconv.Atoi(header)
		events.mu.Lock()
		latest := len(events.events)
		events.mu.Unlock()
		if err != nil || cursor < 0 || cursor > latest {
			writeError(w, http.StatusBadRequest, "Некорректный ID события.")
			return
		}
	}
	controller := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	if controller.Flush() != nil {
		return
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	send := func(frame string) error {
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		defer controller.SetWriteDeadline(time.Time{})
		if _, err := fmt.Fprint(w, frame); err != nil {
			return err
		}
		return controller.Flush()
	}
	for {
		batch, changed, closed := events.next(cursor)
		for _, event := range batch {
			body, _ := json.Marshal(event)
			cursor++
			if send(fmt.Sprintf("id: %d\ndata: %s\n\n", cursor, body)) != nil {
				return
			}
		}
		if len(batch) != 0 {
			continue
		}
		if closed {
			return
		}
		select {
		case <-changed:
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if send(": heartbeat\n\n") != nil {
				return
			}
		}
	}
}

func (s *Server) handlePermission(w http.ResponseWriter, r *http.Request) {
	active := s.getSession(w, r)
	if active == nil {
		return
	}
	var reply struct {
		ID       string
		OptionID string
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&reply); err != nil || reply.ID == "" {
		writeError(w, http.StatusBadRequest, "Нужен ID запроса разрешения.")
		return
	}
	active.mu.Lock()
	defer active.mu.Unlock()
	pending := active.pending[reply.ID]
	if pending == nil || active.turn == nil || active.turn.cancelling {
		writeError(w, http.StatusConflict, "Запрос разрешения уже закрыт.")
		return
	}
	if reply.OptionID == "" {
		active.resolve(pending, acp.PermissionOutcome{Outcome: "cancelled"})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	for _, option := range pending.Options {
		if option.ID == reply.OptionID {
			active.resolve(pending, acp.PermissionOutcome{Outcome: "selected", OptionID: option.ID})
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeError(w, http.StatusBadRequest, "Выберите один из вариантов запроса разрешения.")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
