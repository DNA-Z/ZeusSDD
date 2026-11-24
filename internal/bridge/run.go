package bridge

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"zeussdd/internal/acp"
	"zeussdd/internal/config"
	"zeussdd/internal/localhttp"
	"zeussdd/internal/mcp"
)

func Run() error {
	addr := config.Env("BRIDGE_ADDR", "127.0.0.1:8085")
	home := os.Getenv("ACP_HOME")
	if home == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return fmt.Errorf("ACP profile: %w", err)
		}
		home = filepath.Join(configDir, "ZeusSDD", "gigacode")
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return fmt.Errorf("ACP profile: %w", err)
	}
	workDir, err := filepath.Abs(config.Env("ACP_WORKDIR", "."))
	if err != nil {
		return fmt.Errorf("ACP working directory: %w", err)
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	bridgeConfig := Config{
		Profile: acp.Profile{Home: home, WorkDir: workDir, Launcher: os.Getenv("ZEUSSDD_GIGACODE_PATH")},
		Client:  acp.ClientInfo{Name: "zeussdd", Title: "ZeusSDD", Version: "0.1.0"},
		MCPURL:  os.Getenv("MCP_URL"),
	}
	httpServer := &http.Server{
		Handler:           bridgeRoutes(bridgeConfig, listener.Addr().(*net.TCPAddr), config.WorkflowURL()),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("ACP bridge: http://%s, profile %s, workdir %s", listener.Addr(), home, workDir)
	return httpServer.Serve(listener)
}

func bridgeRoutes(config Config, addr *net.TCPAddr, workflowURL string) http.Handler {
	mux := http.NewServeMux()
	if config.MCPURL == "" {
		local := *addr
		if local.IP.IsUnspecified() {
			local.IP = net.IPv4(127, 0, 0, 1)
			if addr.IP.To4() == nil {
				local.IP = net.IPv6loopback
			}
		}
		config.MCPURL = (&url.URL{Scheme: "http", Host: local.String(), Path: "/mcp"}).String()
		mux.Handle("/mcp", localhttp.Only(mcp.NewServer(workflowURL)))
	}
	mux.Handle("/", NewServer(config).Routes())
	return mux
}
