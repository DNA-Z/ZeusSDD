package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"
)

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
