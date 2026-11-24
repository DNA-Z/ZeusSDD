package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type Profile struct {
	Home     string
	WorkDir  string
	Launcher string
}

func (p Profile) Environ() []string {
	keep := []string{"PATH", "SYSTEMROOT", "WINDIR", "TEMP", "TMP", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "COMSPEC", "PATHEXT", "USERNAME"}
	env := make([]string, 0, len(keep)+1)
	for _, name := range keep {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return append(env, "GIGACODE_HOME="+p.Home)
}

// Prepare creates defaults once so starting a chat preserves CLI login and settings.
func (p Profile) Prepare() error {
	if p.Home == "" {
		return errors.New("acp: profile directory is required")
	}
	if err := os.MkdirAll(p.Home, 0o700); err != nil {
		return err
	}
	path := filepath.Join(p.Home, "settings.json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	const settings = "{\n  \"permissions\": {\n    \"ask\": [\"Bash(*)\", \"Write(*)\", \"Edit(*)\"],\n    \"deny\": [\"Bash(git push:*)\", \"Bash(git commit:*)\"]\n  }\n}\n"
	_, writeErr := io.WriteString(file, settings)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func (p Profile) Command(ctx context.Context) (*exec.Cmd, error) {
	if !filepath.IsAbs(p.WorkDir) {
		return nil, errors.New("acp: working directory must be absolute")
	}
	launcher := p.Launcher
	if launcher == "" {
		launcher = "gigacode"
	} else if !filepath.IsAbs(launcher) {
		return nil, errors.New("acp: ZEUSSDD_GIGACODE_PATH must be an absolute path")
	}
	path, err := exec.LookPath(launcher)
	if err != nil {
		return nil, fmt.Errorf("acp: find GigaCode: %w", err)
	}
	command, err := launcherCommand(ctx, path, p.Environ())
	if err != nil {
		return nil, err
	}
	command.Dir = p.WorkDir
	return command, nil
}

type Transport struct {
	in      io.Writer
	out     *bufio.Reader
	closer  io.Closer
	writeMu sync.Mutex
}

func (p Profile) Open(ctx context.Context) (*Transport, <-chan error, error) {
	command, err := p.Command(ctx)
	if err != nil {
		return nil, nil, err
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, childStdout, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, err
	}
	command.Stdout = childStdout
	command.Stderr = os.Stderr
	dispose, err := startLauncher(command)
	_ = childStdout.Close()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, nil, fmt.Errorf("acp: start agent: %w", err)
	}
	exited := make(chan error, 1)
	go func() {
		err := command.Wait()
		dispose()
		exited <- err
		close(exited)
	}()
	return &Transport{
		in: stdin, out: bufio.NewReader(stdout),
		closer: closeTransport{stdin, stdout},
	}, exited, nil
}

type closeTransport struct {
	stdin  io.Closer
	stdout io.Closer
}

func (p closeTransport) Close() error {
	return errors.Join(p.stdin.Close(), p.stdout.Close())
}

func (t *Transport) Write(message any) error {
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_, err = t.in.Write(append(body, '\n'))
	return err
}

// Read tolerates startup diagnostics emitted by CLI wrappers before JSON-RPC.
func (t *Transport) Read() (map[string]any, error) {
	for {
		line, err := t.out.ReadString('\n')
		var message map[string]any
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.UseNumber()
		if decoder.Decode(&message) == nil && message["jsonrpc"] == "2.0" && decoder.Decode(new(any)) == io.EOF {
			return message, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func (t *Transport) Close() error {
	if t.closer == nil {
		return nil
	}
	return t.closer.Close()
}

func directCommand(ctx context.Context, path string, env []string) *exec.Cmd {
	command := exec.CommandContext(ctx, path, "--acp", "--approval-mode", "default")
	command.Env = env
	return command
}
