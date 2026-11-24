package settings

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
)

const profileCookie = "zeussdd_settings"

func browserProfile(w http.ResponseWriter, r *http.Request) string {
	if cookie, err := r.Cookie(profileCookie); err == nil && len(cookie.Value) == 32 {
		if _, err := hex.DecodeString(cookie.Value); err == nil {
			return cookie.Value
		}
	}
	profile := hex.EncodeToString(randBytes())
	http.SetCookie(w, &http.Cookie{
		Name: profileCookie, Value: profile, Path: "/api/settings", MaxAge: 365 * 24 * 60 * 60,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
	})
	return profile
}

func randBytes() []byte {
	var value [16]byte
	rand.Read(value[:])
	return value[:]
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func routes(state *store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		var value settings
		var err error
		switch r.Method {
		case http.MethodGet:
			value, err = state.get(r.Context(), browserProfile(w, r))
		case http.MethodPut:
			mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if mediaType != "application/json" {
				writeError(w, http.StatusUnsupportedMediaType, "Ожидаются настройки в формате JSON.")
				return
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
			if decoder.Decode(&value) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) || (value.Theme != "dark" && value.Theme != "light") {
				writeError(w, http.StatusBadRequest, "Выберите светлую или тёмную тему.")
				return
			}
			err = state.save(r.Context(), browserProfile(w, r), value)
		default:
			w.Header().Set("Allow", "GET, PUT")
			writeError(w, http.StatusMethodNotAllowed, "Метод недоступен.")
			return
		}
		if err != nil {
			log.Print(err)
			writeError(w, http.StatusInternalServerError, "Не удалось обработать настройки.")
			return
		}
		_ = json.NewEncoder(w).Encode(value)
	})
	return mux
}
