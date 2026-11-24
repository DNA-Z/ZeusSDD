package config

import "os"

const DefaultWorkflowAddr = "127.0.0.1:8081"

func WorkflowURL() string {
	return Env("WORKFLOW_URL", "http://"+DefaultWorkflowAddr)
}

func Env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
