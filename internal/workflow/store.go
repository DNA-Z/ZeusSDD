package workflow

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type message struct {
	ID        int64     `json:"id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
}

type messageStore struct {
	pool   *pgxpool.Pool
	mu     sync.Mutex
	notes  map[string][]message
	nextID int64
}

func newMessageStore(ctx context.Context, dsn string) (*messageStore, error) {
	store := &messageStore{notes: make(map[string][]message)}
	if dsn == "" {
		return store, nil
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	_, err = pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS task_messages (
		id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		task_key text NOT NULL,
		body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 4000),
		created_at timestamptz NOT NULL DEFAULT now()
	)`)
	if err != nil {
		pool.Close()
		return nil, err
	}
	store.pool = pool
	return store, nil
}

func (s *messageStore) close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

func (s *messageStore) list(ctx context.Context, key string) ([]message, error) {
	result := make([]message, 0)
	if s.pool == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		return append(result, s.notes[key]...), nil
	}
	rows, err := s.pool.Query(ctx, "SELECT id, body, created_at FROM task_messages WHERE task_key = $1 ORDER BY id", key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item message
		if err := rows.Scan(&item.ID, &item.Text, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *messageStore) add(ctx context.Context, key, value string) (message, error) {
	item := message{Text: value}
	if s.pool != nil {
		err := s.pool.QueryRow(ctx, "INSERT INTO task_messages (task_key, body) VALUES ($1, $2) RETURNING id, created_at", key, value).Scan(&item.ID, &item.CreatedAt)
		return item, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	item.ID = s.nextID
	item.CreatedAt = time.Now().UTC()
	s.notes[key] = append(s.notes[key], item)
	return item, nil
}
