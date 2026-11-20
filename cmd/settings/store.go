package main

import (
	"context"
	"errors"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type settings struct {
	Theme string `json:"theme"`
}

type store struct {
	pool   *pgxpool.Pool
	mu     sync.Mutex
	themes map[string]string
}

func newStore(ctx context.Context, dsn string) (*store, error) {
	state := &store{themes: make(map[string]string)}
	if dsn == "" {
		return state, nil
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	_, err = pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS browser_settings (
		profile text PRIMARY KEY,
		theme text NOT NULL CHECK (theme IN ('dark','light'))
	)`)
	if err != nil {
		pool.Close()
		return nil, err
	}
	state.pool = pool
	return state, nil
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
