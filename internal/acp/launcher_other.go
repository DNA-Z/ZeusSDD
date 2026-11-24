//go:build !windows

package acp

import (
	"context"
	"os/exec"
)

func launcherCommand(ctx context.Context, path string, env []string) (*exec.Cmd, error) {
	return directCommand(ctx, path, env), nil
}

func startLauncher(command *exec.Cmd) (func(), error) {
	return func() {}, command.Start()
}
