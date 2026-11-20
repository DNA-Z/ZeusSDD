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
	state, err := newStore(ctx, os.Getenv("DATABASE_URL"))
	cancel()
	if err != nil {
		log.Fatal(err)
	}
	addr := os.Getenv("SETTINGS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8083"
	}
	server := &http.Server{Addr: addr, Handler: routes(state), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	log.Printf("Settings: %s", addr)
	log.Fatal(server.ListenAndServe())
}
