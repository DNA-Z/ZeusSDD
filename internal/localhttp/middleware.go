package localhttp

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Only restricts access to loopback clients and same-origin browser requests.
func Only(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localRequest(r) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "ACP доступен только локальному приложению."})
			return
		}
		handler.ServeHTTP(w, r)
	})
}

func localRequest(r *http.Request) bool {
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(remote).IsLoopback() {
		return false
	}
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = strings.Trim(r.Host, "[]")
	}
	if !strings.EqualFold(host, "localhost") && !net.ParseIP(host).IsLoopback() {
		return false
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && strings.EqualFold(parsed.Host, r.Host)
	}
	return true
}
