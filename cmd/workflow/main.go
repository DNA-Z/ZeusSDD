package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed workspace.json
var sample json.RawMessage

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

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Print(err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func routes(store *messageStore) http.Handler {
	var workspace struct {
		Releases []struct {
			Tasks []struct{ Key string } `json:"tasks"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(sample, &workspace); err != nil {
		panic(fmt.Errorf("invalid demo workspace: %w", err))
	}
	keys := make(map[string]bool)
	for _, release := range workspace.Releases {
		for _, task := range release.Tasks {
			keys[task.Key] = true
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspace", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, sample)
	})
	mux.HandleFunc("/api/tasks/{key}/messages", func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		if !keys[key] {
			writeError(w, http.StatusNotFound, "Задача не найдена.")
			return
		}
		switch r.Method {
		case http.MethodGet:
			items, err := store.list(r.Context(), key)
			if err != nil {
				log.Print(err)
				writeError(w, http.StatusInternalServerError, "Не удалось загрузить сообщения.")
				return
			}
			writeJSON(w, http.StatusOK, items)
		case http.MethodPost:
			var input struct {
				Text string `json:"text"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
			if err := decoder.Decode(&input); err != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
				writeError(w, http.StatusBadRequest, "Ожидается сообщение в формате JSON.")
				return
			}
			input.Text = strings.TrimSpace(input.Text)
			if input.Text == "" || utf8.RuneCountInString(input.Text) > 4000 || strings.ContainsRune(input.Text, 0) {
				writeError(w, http.StatusBadRequest, "Сообщение должно содержать от 1 до 4000 символов.")
				return
			}
			item, err := store.add(r.Context(), key, input.Text)
			if err != nil {
				log.Print(err)
				writeError(w, http.StatusInternalServerError, "Не удалось отправить сообщение.")
				return
			}
			writeJSON(w, http.StatusCreated, item)
		default:
			w.Header().Set("Allow", "GET, POST")
			writeError(w, http.StatusMethodNotAllowed, "Метод недоступен.")
		}
	})
	return mux
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	store, err := newMessageStore(ctx, os.Getenv("DATABASE_URL"))
	cancel()
	if err != nil {
		log.Fatal(err)
	}
	if store.pool == nil {
		log.Print("Demo mode: messages are stored in memory")
	} else {
		defer store.pool.Close()
	}
	addr := os.Getenv("WORKFLOW_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8081"
	}
	server := &http.Server{Addr: addr, Handler: routes(store), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second}
	log.Printf("Workflow: %s", addr)
	log.Fatal(server.ListenAndServe())
}
