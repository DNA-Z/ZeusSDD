package gateway

import (
	"log"
	"net/http"
	"time"

	"zeussdd/internal/config"
)

func Run() error {
	settings, err := upstreamProxy("SETTINGS_URL", config.Env("SETTINGS_URL", "http://127.0.0.1:8083"), "Сервис настроек недоступен.")
	if err != nil {
		return err
	}
	acp, err := upstreamProxy("BRIDGE_URL", config.Env("BRIDGE_URL", "http://127.0.0.1:8085"), "Сервис GigaCode недоступен. Запустите go run ./cmd/bridge.")
	if err != nil {
		return err
	}
	workflow, err := upstreamProxy("WORKFLOW_URL", config.WorkflowURL(), "Сервис задач недоступен.")
	if err != nil {
		return err
	}

	addr := config.Env("GATEWAY_ADDR", "127.0.0.1:8080")
	server := &http.Server{
		Addr:              addr,
		Handler:           routes(settings, workflow, acp),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      20 * time.Second,
	}
	log.Printf("ZeusSDD: http://%s", addr)
	return server.ListenAndServe()
}
