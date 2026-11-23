package main

import (
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	addr := os.Getenv("MCP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8084"
	}
	base := os.Getenv("WORKFLOW_URL")
	if base == "" {
		base = "http://127.0.0.1:8081"
	}
	server := &http.Server{Addr: addr, Handler: newServer(base), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second}
	log.Printf("MCP: http://%s/mcp -> %s", addr, base)
	log.Fatal(server.ListenAndServe())
}
