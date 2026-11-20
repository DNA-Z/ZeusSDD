package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const profileCookie = "zeussdd_settings"

type settings struct {
	Theme string `json:"theme"`
}

type store struct {
	pool   *pgxpool.Pool
	mu     sync.Mutex
	themes map[string]string
}

func (s *store) get(ctx context.Context, profile string) (settings, error) {
	var theme string
	if s.pool != nil {
		err := s.pool.QueryRow(ctx, "SELECT theme FROM browser_settings WHERE profile = $1", profile).Scan(&theme)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return settings{}, err
		}
	} else {
		s.mu.Lock()
		theme = s.themes[profile]
		s.mu.Unlock()
	}
	if theme == "" {
		theme = "dark"
	}
	return settings{Theme: theme}, nil
}

func (s *store) save(ctx context.Context, profile string, value settings) error {
	if s.pool != nil {
		_, err := s.pool.Exec(ctx, `INSERT INTO browser_settings (profile, theme) VALUES ($1, $2)
			ON CONFLICT (profile) DO UPDATE SET theme = EXCLUDED.theme`, profile, value.Theme)
		return err
	}
	s.mu.Lock()
	s.themes[profile] = value.Theme
	s.mu.Unlock()
	return nil
}

func browserProfile(w http.ResponseWriter, r *http.Request) string {
	if cookie, err := r.Cookie(profileCookie); err == nil && len(cookie.Value) == 32 {
		if _, err := hex.DecodeString(cookie.Value); err == nil {
			return cookie.Value
		}
	}
	profile := hex.EncodeToString(randBytes())
	http.SetCookie(w, &http.Cookie{
		Name: profileCookie, Value: profile, Path: "/api/settings", MaxAge: 365 * 24 * 60 * 60,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
	})
	return profile
}

func randBytes() []byte {
	var value [16]byte
	rand.Read(value[:])
	return value[:]
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (s *store) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	var value settings
	var err error
	switch r.Method {
	case http.MethodGet:
		value, err = s.get(r.Context(), browserProfile(w, r))
	case http.MethodPut:
		mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mediaType != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "Ожидаются настройки в формате JSON.")
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		if decoder.Decode(&value) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) || (value.Theme != "dark" && value.Theme != "light") {
			writeError(w, http.StatusBadRequest, "Выберите светлую или тёмную тему.")
			return
		}
		err = s.save(r.Context(), browserProfile(w, r), value)
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeError(w, http.StatusMethodNotAllowed, "Метод недоступен.")
		return
	}
	if err != nil {
		log.Print(err)
		writeError(w, http.StatusInternalServerError, "Не удалось обработать настройки.")
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

func main() {
	state := &store{themes: make(map[string]string)}
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		pool, err := pgxpool.New(ctx, dsn)
		if err == nil {
			_, err = pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS browser_settings (
				profile text PRIMARY KEY,
				theme text NOT NULL CHECK (theme IN ('dark','light'))
			)`)
		}
		cancel()
		if err != nil {
			log.Fatal(err)
		}
		state.pool = pool
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/settings", state.serve)
	addr := os.Getenv("SETTINGS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8083"
	}
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	log.Printf("Settings: %s", addr)
	log.Fatal(server.ListenAndServe())
}
