package acp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileArgsAndEnv(t *testing.T) {
	t.Setenv("GIGACODE_API_KEY", "must-not-leak")
	t.Setenv("OPENAI_API_KEY", "must-not-leak")

	profile := Profile{Home: "/tmp/acp-home", PermissionMode: "default"}
	if got := strings.Join(profile.Args(), " "); got != "--acp --approval-mode default" {
		t.Fatalf("unexpected args: %s", got)
	}

	env := profile.Environ()
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "must-not-leak") {
		t.Fatalf("provider credentials leaked into the child env: %s", joined)
	}
	if !strings.Contains(joined, "GIGACODE_HOME=/tmp/acp-home") {
		t.Fatalf("home override missing: %s", joined)
	}
	if !strings.Contains(joined, "GIGACODE_PACKAGE_IDENTITY="+packageIdentity) {
		t.Fatalf("package identity missing: %s", joined)
	}
}

func TestWriteSettingsIsIsolated(t *testing.T) {
	home := t.TempDir()
	profile := Profile{
		Home:       home,
		AllowedMCP: []MCPServer{{Name: "zeussdd", Command: "http://127.0.0.1:8084/mcp"}},
	}
	if err := profile.WriteSettings(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(home, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "zeussdd") {
		t.Fatalf("allowed server missing: %s", body)
	}
	for _, forbidden := range []string{"confluence", "sprut", "jira"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("%s must not appear in the isolated profile: %s", forbidden, body)
		}
	}
	if !strings.Contains(string(body), "git push") {
		t.Fatalf("deny rules missing: %s", body)
	}
}

func TestCommandRequiresLauncher(t *testing.T) {
	profile := Profile{WorkDir: t.TempDir()}
	if _, err := profile.Command(t.Context()); err == nil {
		t.Fatal("expected an error without a launcher path")
	}
	profile.NodePath, profile.EntryPoint = "node.exe", "cli-entry.js"
	command, err := profile.Command(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if command.Env[0] == os.Environ()[0] && len(os.Environ()) > 0 {
		// Environ must be derived from the allowlist, not the caller's env.
		t.Fatal("child env was inherited")
	}
}

func TestSelectAllowOnce(t *testing.T) {
	options := []any{
		map[string]any{"optionId": "always", "kind": "allow_always"},
		map[string]any{"optionId": "once", "kind": "allow_once"},
	}
	if got := SelectAllowOnce(options); got.OptionID != "once" {
		t.Fatalf("expected allow_once to win, got %+v", got)
	}
	if got := SelectAllowOnce(nil); got.Outcome != "cancelled" {
		t.Fatalf("expected cancelled without options, got %+v", got)
	}
}
