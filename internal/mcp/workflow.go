package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (s *server) listTasks(ctx context.Context) (string, error) {
	var state struct {
		Releases []struct {
			ID    string `json:"id"`
			Tasks []struct {
				Key       string `json:"key"`
				Title     string `json:"title"`
				Status    string `json:"status"`
				Kind      string `json:"kind"`
				Branch    string `json:"branch"`
				Developer string `json:"developer"`
				Files     []any  `json:"files"`
			} `json:"tasks"`
		} `json:"releases"`
	}
	if err := s.get(ctx, "/api/workspace", &state); err != nil {
		return "", err
	}
	var builder strings.Builder
	for _, release := range state.Releases {
		for _, task := range release.Tasks {
			fmt.Fprintf(&builder, "%s · %s · %s · %s · %s · файлов: %d\n",
				task.Key, task.Title, statusName(task.Status), release.ID, task.Branch, len(task.Files))
		}
	}
	return builder.String(), nil
}

func statusName(status string) string {
	switch status {
	case "todo":
		return "не взята в работу"
	case "progress":
		return "в работе"
	case "testing":
		return "на тестировании"
	default:
		return status
	}
}

func (s *server) getTask(ctx context.Context, key string) (string, error) {
	var detail struct {
		Task struct {
			Key          string `json:"key"`
			Title        string `json:"title"`
			Status       string `json:"status"`
			Kind         string `json:"kind"`
			Branch       string `json:"branch"`
			Developer    string `json:"developer"`
			Analyst      string `json:"analyst"`
			Tester       string `json:"tester"`
			Summary      string `json:"summary"`
			Dependencies []struct {
				Key       string `json:"key"`
				Condition string `json:"condition"`
			} `json:"dependencies"`
			Files []struct {
				Path  string      `json:"path"`
				Lines [][2]string `json:"lines"`
			} `json:"files"`
		} `json:"task"`
		Release struct {
			ID string `json:"id"`
		} `json:"release"`
	}
	if err := s.get(ctx, "/api/tasks/"+url.PathEscape(key), &detail); err != nil {
		return "", err
	}
	task := detail.Task
	var builder strings.Builder
	fmt.Fprintf(&builder, "%s — %s\nРелиз: %s · статус: %s · тип: %s\nВетка: %s\nРазработчик: %s · аналитик: %s", task.Key, task.Title, detail.Release.ID, statusName(task.Status), task.Kind, task.Branch, task.Developer, task.Analyst)
	if task.Tester != "" {
		fmt.Fprintf(&builder, " · тестировщик: %s", task.Tester)
	}
	fmt.Fprintf(&builder, "\n\n%s\n", task.Summary)
	for _, dependency := range task.Dependencies {
		fmt.Fprintf(&builder, "\nЗависимость %s: %s", dependency.Key, dependency.Condition)
	}
	if len(task.Files) == 0 {
		builder.WriteString("\nИзменённых файлов нет.")
	}
	for _, file := range task.Files {
		fmt.Fprintf(&builder, "\n\n### %s\n", file.Path)
		for _, line := range file.Lines {
			kind, text := line[0], line[1]
			switch kind {
			case "add":
				fmt.Fprintf(&builder, "+ %s\n", text)
			case "remove":
				fmt.Fprintf(&builder, "- %s\n", text)
			default:
				fmt.Fprintf(&builder, "  %s\n", text)
			}
		}
	}
	return builder.String(), nil
}

type chatMessage struct {
	ID        int64     `json:"id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *server) listMessages(ctx context.Context, key string) ([]chatMessage, error) {
	var items []chatMessage
	if err := s.get(ctx, "/api/tasks/"+url.PathEscape(key)+"/messages", &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *server) postMessage(ctx context.Context, key, value string) (chatMessage, error) {
	body, err := json.Marshal(map[string]string{"text": value})
	if err != nil {
		return chatMessage{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/api/tasks/"+url.PathEscape(key)+"/messages", bytes.NewReader(body))
	if err != nil {
		return chatMessage{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	var message chatMessage
	if err := s.do(request, &message); err != nil {
		return chatMessage{}, err
	}
	return message, nil
}

func (s *server) get(ctx context.Context, path string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+path, nil)
	if err != nil {
		return err
	}
	return s.do(request, target)
}

func (s *server) do(request *http.Request, target any) error {
	response, err := s.workflow.Do(request)
	if err != nil {
		return fmt.Errorf("Сервис задач недоступен.")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		var failure struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 4<<10)).Decode(&failure) == nil && failure.Error != "" {
			return fmt.Errorf("%s", failure.Error)
		}
		return fmt.Errorf("Сервис задач ответил кодом %d.", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return fmt.Errorf("Сервис задач вернул неожиданный ответ.")
	}
	return nil
}
