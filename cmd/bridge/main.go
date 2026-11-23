package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"zeussdd/internal/acp"
	"zeussdd/internal/bridge"
)

func main() {
	addr := os.Getenv("BRIDGE_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8085"
	}
	home := os.Getenv("ACP_HOME")
	if home == "" {
		home = filepath.Join(os.TempDir(), "zeussdd-acp")
	}
	workDir := os.Getenv("ACP_WORKDIR")
	if workDir == "" {
		workDir = "."
	}
	if abs, err := filepath.Abs(workDir); err == nil {
		workDir = abs
	}

	nodePath, entryPoint, err := acp.DetectLauncher(root())
	if err != nil {
		log.Fatalf("ACP launcher: %v", err)
	}

	profile := acp.Profile{
		Home:       home,
		WorkDir:    workDir,
		NodePath:   nodePath,
		EntryPoint: entryPoint,
	}
	if mcpURL := os.Getenv("MCP_URL"); mcpURL != "" {
		profile.AllowedMCP = []acp.MCPServer{{Name: "zeussdd", Command: mcpURL}}
	}

	server := bridge.NewServer(bridge.Config{
		Profile:      profile,
		Client:       acp.ClientInfo{Name: "zeussdd", Title: "ZeusSDD", Version: "0.1.0"},
		ApprovalMode: "default",
	})

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           server.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("ACP bridge: http://%s, profile %s, workdir %s", addr, home, workDir)
	log.Fatal(httpServer.ListenAndServe())
}

// root resolves the GigaCode installation directory for launcher autodetection.
// GIGACODE_ROOT wins; the packaged layout puts runtime/ and lib/ next to bin/.
func root() string {
	if value := os.Getenv("GIGACODE_ROOT"); value != "" {
		return value
	}
	for _, dir := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432")} {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, "GigaCode")
		if _, err := os.Stat(filepath.Join(candidate, "lib", "cli-entry.js")); err == nil {
			return candidate
		}
	}
	return filepath.Join(os.Getenv("ProgramFiles"), "GigaCode")
}
