package acp

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProfileEnviron(t *testing.T) {
	t.Setenv("GIGACODE_API_KEY", "must-not-leak")
	t.Setenv("OPENAI_API_KEY", "must-not-leak")
	profile := Profile{Home: t.TempDir()}
	joined := strings.Join(profile.Environ(), "\n")
	if strings.Contains(joined, "must-not-leak") || !strings.Contains(joined, "GIGACODE_HOME="+profile.Home) {
		t.Fatal("profile environment did not isolate credentials and override GigaCode home")
	}
}

func TestPreparePreservesSettings(t *testing.T) {
	profile := Profile{Home: t.TempDir()}
	if err := profile.Prepare(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(profile.Home, "settings.json")
	body, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(body), "git push") {
		t.Fatalf("permission defaults missing: %s, %v", body, err)
	}
	const customized = `{"theme":"custom"}`
	if err := os.WriteFile(path, []byte(customized), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := profile.Prepare(); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(path)
	if err != nil || string(body) != customized {
		t.Fatalf("settings were overwritten: %s, %v", body, err)
	}
}

func TestLauncherLookup(t *testing.T) {
	dir := t.TempDir()
	launcher := filepath.Join(dir, "gigacode")
	if runtime.GOOS == "windows" {
		launcher += ".cmd"
		t.Setenv("PATHEXT", ".EXE;.CMD")
	}
	if err := os.WriteFile(launcher, []byte{}, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	for _, override := range []string{"", launcher} {
		profile := Profile{WorkDir: dir, Launcher: override}
		command, err := profile.Command(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if command.Dir != dir {
			t.Fatalf("working directory: %s", command.Dir)
		}
		if runtime.GOOS != "windows" && (command.Path != launcher || command.Args[1] != "--acp") {
			t.Fatalf("unexpected launcher command: %v", command.Args)
		}
	}
	profile := Profile{WorkDir: dir, Launcher: "./gigacode"}
	if _, err := profile.Command(t.Context()); err == nil {
		t.Fatal("relative launcher override was accepted")
	}
}
