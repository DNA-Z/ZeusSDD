// Package acp launches a GigaCode agent in ACP mode with an isolated profile.
//
// The profile is what makes the launch safe: GigaCode resolves its global
// settings (MCP servers, skills, extensions, permission rules) from the user's
// home directory, so an unconstrained run would hand every one of the user's
// integrations to the agent. GigaCodeHome points the process at a directory we
// own, where only explicitly allowed customizations exist.
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
	"runtime"
	"strings"
	"sync"
)

type Profile struct {
	// Home becomes GIGACODE_HOME: an isolated global-state root.
	Home string
	// WorkDir is where the agent works; it is also the session cwd.
	WorkDir string
	// AllowedMCP lists MCP servers that the isolated profile may start. Every
	// other server from the user's real home is invisible to the agent.
	AllowedMCP []MCPServer
	// PermissionMode is applied as --approval-mode, e.g. "default" so the
	// client still receives session/request_permission for each tool call.
	PermissionMode string
	// NodePath and EntryPoint override autodetection of the launcher.
	NodePath   string
	EntryPoint string
}

type MCPServer struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
}

// Args is the command line for the packaged launcher. --acp is stable and the
// launcher is required: an editor must not spawn the JS entrypoint directly.
func (p Profile) Args() []string {
	args := []string{"--acp"}
	if p.PermissionMode != "" {
		args = append(args, "--approval-mode", p.PermissionMode)
	}
	return args
}

// Environ builds the child environment from scratch instead of inheriting the
// caller's. Provider API keys must not reach the ACP process (backend auth
// handles the model), and GIGACODE_HOME must point at the isolated root.
func (p Profile) Environ() []string {
	keep := []string{"PATH", "SYSTEMROOT", "SystemRoot", "WINDIR", "TEMP", "TMP", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "COMSPEC", "PATHEXT", "USERNAME"}
	env := make([]string, 0, len(keep)+2)
	for _, name := range keep {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return append(env, "GIGACODE_HOME="+p.Home, "GIGACODE_PACKAGE_IDENTITY="+packageIdentity)
}

// WriteSettings materialises the isolated profile: only the MCP servers listed
// in AllowedMCP and explicit permission rules, nothing inherited from the user.
func (p Profile) WriteSettings() error {
	if err := os.MkdirAll(p.Home, 0o700); err != nil {
		return err
	}
	servers := make(map[string]any, len(p.AllowedMCP))
	for _, server := range p.AllowedMCP {
		entry := map[string]any{"command": server.Command, "args": server.Args}
		if len(server.Env) > 0 {
			pairs := make([]string, 0, len(server.Env))
			for key, value := range server.Env {
				pairs = append(pairs, key+"="+value)
			}
			entry["env"] = pairs
		}
		servers[server.Name] = entry
	}
	settings := map[string]any{
		"mcpServers": servers,
		"permissions": map[string]any{
			"ask": []string{"Bash(*)", "Write(*)", "Edit(*)"},
			"deny": []string{
				"Bash(git push:*)",
				"Bash(git commit:*)",
			},
		},
	}
	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(p.Home, "settings.json"), append(body, '\n'), 0o600)
}

const packageIdentity = "4accbf78da580a2e40cac51ea0596d42207bb1c8e156d35ddef47175a676e675"

// Command builds the child process. It runs the packaged launcher (a .cmd on
// Windows, which needs cmd.exe) rather than cli-entry.js.
func (p Profile) Command(ctx context.Context) (*exec.Cmd, error) {
	if p.WorkDir == "" {
		return nil, errors.New("acp: work dir is required")
	}
	if p.NodePath == "" || p.EntryPoint == "" {
		return nil, errors.New("acp: launcher node path and entry point are required")
	}
	args := append([]string{p.EntryPoint}, p.Args()...)
	command := exec.CommandContext(ctx, p.NodePath, args...)
	command.Dir = p.WorkDir
	command.Env = p.Environ()
	return command, nil
}

// DetectLauncher resolves the packaged runtime next to the installed CLI so the
// caller does not have to hardcode an installation path.
func DetectLauncher(root string) (nodePath, entryPoint string, err error) {
	nodePath = filepath.Join(root, "runtime", "node", "node.exe")
	if runtime.GOOS != "windows" {
		nodePath = filepath.Join(root, "runtime", "node", "bin", "node")
	}
	entryPoint = filepath.Join(root, "lib", "cli-entry.js")
	for path, kind := range map[string]string{nodePath: "node", entryPoint: "cli entry"} {
		if _, statErr := os.Stat(path); statErr != nil {
			return "", "", fmt.Errorf("acp: %s not found at %s", kind, path)
		}
	}
	return nodePath, entryPoint, nil
}

// Transport is a newline-delimited JSON-RPC connection over stdio.
type Transport struct {
	in     io.Writer
	out    *bufio.Reader
	closer io.Closer

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
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return nil, nil, fmt.Errorf("acp: start agent: %w", err)
	}
	exited := make(chan error, 1)
	go func() {
		exited <- command.Wait()
		close(exited)
	}()
	return &Transport{in: stdin, out: bufio.NewReaderSize(stdout, 1<<20), closer: stdin}, exited, nil
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

// Read returns the next JSON-RPC message. Lines that are not JSON come from the
// agent's own diagnostics and are skipped rather than treated as protocol
// errors, which keeps a noisy stderr-free run from killing the session.
func (t *Transport) Read() (map[string]any, error) {
	for {
		line, err := t.out.ReadString('\n')
		if err != nil {
			if strings.TrimSpace(line) != "" {
				if message, decodeErr := decodeMessage(line); decodeErr == nil {
					return message, nil
				}
			}
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		message, err := decodeMessage(line)
		if err != nil {
			continue
		}
		return message, nil
	}
}

func decodeMessage(line string) (map[string]any, error) {
	var message map[string]any
	if err := json.Unmarshal([]byte(line), &message); err != nil {
		return nil, err
	}
	if message["jsonrpc"] == nil {
		return nil, errors.New("not a json-rpc message")
	}
	return message, nil
}

func (t *Transport) Close() error {
	if t.closer == nil {
		return nil
	}
	return t.closer.Close()
}
