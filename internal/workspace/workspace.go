package workspace

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed workspace.json
var sample []byte

type DiffLine [2]string

type File struct {
	Name  string     `json:"name"`
	Path  string     `json:"path"`
	Lines []DiffLine `json:"lines"`
}

type Dependency struct {
	Key       string `json:"key"`
	Condition string `json:"condition"`
}

type Task struct {
	Key          string       `json:"key"`
	Title        string       `json:"title"`
	Status       string       `json:"status"`
	HasSpec      bool         `json:"hasSpec"`
	Developer    string       `json:"developer"`
	Analyst      string       `json:"analyst"`
	Tester       string       `json:"tester,omitempty"`
	Summary      string       `json:"summary"`
	Branch       string       `json:"branch"`
	Kind         string       `json:"kind"`
	Dependencies []Dependency `json:"dependencies"`
	Files        []File       `json:"files"`
}

type Release struct {
	ID      string `json:"id"`
	Sprint  string `json:"sprint"`
	Current bool   `json:"current"`
	Tasks   []Task `json:"tasks"`
}

type Workspace struct {
	Releases []Release `json:"releases"`
}

func Load() (Workspace, error) {
	var value Workspace
	decoder := json.NewDecoder(bytes.NewReader(sample))
	if err := decoder.Decode(&value); err != nil {
		return Workspace{}, fmt.Errorf("invalid demo workspace: %w", err)
	}
	return value, nil
}

func (w Workspace) Task(key string) (Task, Release, bool) {
	for _, release := range w.Releases {
		for _, task := range release.Tasks {
			if task.Key == key {
				return task, release, true
			}
		}
	}
	return Task{}, Release{}, false
}
