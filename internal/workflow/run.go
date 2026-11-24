package workflow

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"zeussdd/internal/config"
	"zeussdd/internal/workspace"
)

func Run() error {
	state, err := workspace.Load()
	if err != nil {
		return err
	}

	dsn := os.Getenv("DATABASE_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	store, err := newMessageStore(ctx, dsn)
	cancel()
	if err != nil {
		return err
	}
	defer store.close()

	if dsn == "" {
		log.Print("Demo mode: messages are stored in memory")
	}
	addr := config.Env("WORKFLOW_ADDR", config.DefaultWorkflowAddr)
	server := &http.Server{
		Addr:              addr,
		Handler:           routes(state, store),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
	}
	log.Printf("Workflow: %s", addr)
	return server.ListenAndServe()
}
