package gateway

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"

	"zeussdd/internal/localhttp"
)

func upstreamProxy(env, upstream, unavailable string) (http.Handler, error) {
	target, err := url.Parse(upstream)
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, fmt.Errorf("invalid %s", env)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		log.Print(err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":` + strconv.Quote(unavailable) + `}`))
	}
	return proxy, nil
}

func routes(settings, workflow, acp http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/settings", settings)
	mux.Handle("/api/acp/", localhttp.Only(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prompts and SSE can outlive the Gateway's normal HTTP write timeout.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		acp.ServeHTTP(w, r)
	})))
	mux.Handle("/api/", workflow)
	mux.Handle("/", http.FileServer(http.Dir("frontend/dist")))
	return mux
}
