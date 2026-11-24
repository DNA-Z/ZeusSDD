package settings

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"zeussdd/internal/config"
)

func Run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	state, err := newStore(ctx, os.Getenv("DATABASE_URL"))
	cancel()
	if err != nil {
		return err
	}
	defer state.close()

	addr := config.Env("SETTINGS_ADDR", "127.0.0.1:8083")
	server := &http.Server{
		Addr:              addr,
		Handler:           routes(state),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	log.Printf("Settings: %s", addr)
	return server.ListenAndServe()
}
