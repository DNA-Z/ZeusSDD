package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
)

func upstreamProxy(env, fallback, unavailable string) http.Handler {
	upstream := os.Getenv(env)
	if upstream == "" {
		upstream = fallback
	}
	target, err := url.Parse(upstream)
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		log.Fatalf("invalid %s", env)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		log.Print(err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":` + strconv.Quote(unavailable) + `}`))
	}
	return proxy
}

func routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/settings", upstreamProxy("SETTINGS_URL", "http://127.0.0.1:8083", "Сервис настроек недоступен."))
	mux.Handle("/api/", upstreamProxy("WORKFLOW_URL", "http://127.0.0.1:8081", "Сервис задач недоступен."))
	mux.Handle("/", http.FileServer(http.Dir("frontend/dist")))
	return mux
}
