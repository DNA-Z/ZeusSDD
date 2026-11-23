package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"zeussdd/internal/workspace"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Print(err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func routes(store *messageStore) http.Handler {
	state, err := workspace.Load()
	if err != nil {
		panic(err)
	}
	keys := make(map[string]bool)
	for _, release := range state.Releases {
		for _, task := range release.Tasks {
			keys[task.Key] = true
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workspace", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, state)
	})
	mux.HandleFunc("GET /api/tasks/{key}", func(w http.ResponseWriter, r *http.Request) {
		task, release, ok := state.Task(r.PathValue("key"))
		if !ok {
			writeError(w, http.StatusNotFound, "Задача не найдена.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"task": task, "release": release})
	})
	mux.HandleFunc("/api/tasks/{key}/messages", func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		if !keys[key] {
			writeError(w, http.StatusNotFound, "Задача не найдена.")
			return
		}
		switch r.Method {
		case http.MethodGet:
			items, err := store.list(r.Context(), key)
			if err != nil {
				log.Print(err)
				writeError(w, http.StatusInternalServerError, "Не удалось загрузить сообщения.")
				return
			}
			writeJSON(w, http.StatusOK, items)
		case http.MethodPost:
			var input struct {
				Text string `json:"text"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
			if err := decoder.Decode(&input); err != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
				writeError(w, http.StatusBadRequest, "Ожидается сообщение в формате JSON.")
				return
			}
			input.Text = strings.TrimSpace(input.Text)
			if input.Text == "" || utf8.RuneCountInString(input.Text) > 4000 || strings.ContainsRune(input.Text, 0) {
				writeError(w, http.StatusBadRequest, "Сообщение должно содержать от 1 до 4000 символов.")
				return
			}
			item, err := store.add(r.Context(), key, input.Text)
			if err != nil {
				log.Print(err)
				writeError(w, http.StatusInternalServerError, "Не удалось отправить сообщение.")
				return
			}
			writeJSON(w, http.StatusCreated, item)
		default:
			w.Header().Set("Allow", "GET, POST")
			writeError(w, http.StatusMethodNotAllowed, "Метод недоступен.")
		}
	})
	return mux
}
