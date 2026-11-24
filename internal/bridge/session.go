package bridge

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"sync"

	"zeussdd/internal/acp"
)

type Event struct {
	Kind    string          `json:"kind"`
	Session string          `json:"session"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type journal struct {
	mu      sync.Mutex
	events  []Event
	changed chan struct{}
	closed  bool
}

func (j *journal) publish(event Event) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return
	}
	j.events = append(j.events, event)
	close(j.changed)
	j.closed = event.Kind == "closed"
	if !j.closed {
		j.changed = make(chan struct{})
	}
}

func (j *journal) next(cursor int) ([]Event, <-chan struct{}, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	start := min(cursor, len(j.events))
	end := min(start+64, len(j.events))
	return j.events[start:end], j.changed, j.closed
}

type permission struct {
	acp.PermissionRequest
	ID     string `json:"id"`
	answer chan acp.PermissionOutcome
}

type turn struct {
	sent, done chan struct{}
	cancelling bool
}

type session struct {
	id          string
	agentID     string
	taskContext string
	ctx         context.Context
	stop        context.CancelFunc
	client      *acp.Client
	events      journal

	mu      sync.Mutex
	turn    *turn
	pending map[string]*permission
}

func (s *session) publish(kind string, payload any) {
	raw, _ := json.Marshal(payload)
	s.events.publish(Event{Kind: kind, Session: s.id, Payload: raw})
}

func (s *session) onPermission(request acp.PermissionRequest) acp.PermissionOutcome {
	cancelled := acp.PermissionOutcome{Outcome: "cancelled"}
	s.mu.Lock()
	if s.turn == nil || s.turn.cancelling || request.SessionID != s.agentID || len(request.Options) == 0 {
		s.mu.Unlock()
		return cancelled
	}
	pending := &permission{
		PermissionRequest: request, ID: rand.Text(), answer: make(chan acp.PermissionOutcome, 1),
	}
	s.pending[pending.ID] = pending
	s.publish("permission", pending)
	s.mu.Unlock()

	select {
	case outcome := <-pending.answer:
		return outcome
	case <-s.ctx.Done():
		return cancelled
	}
}

// Call with s.mu held so each request can be answered only once.
func (s *session) resolve(p *permission, outcome acp.PermissionOutcome) {
	delete(s.pending, p.ID)
	s.publish("permission_resolved", map[string]string{"id": p.ID})
	p.answer <- outcome
}

func (s *session) cancelPermissions() {
	for _, pending := range s.pending {
		s.resolve(pending, acp.PermissionOutcome{Outcome: "cancelled"})
	}
}
