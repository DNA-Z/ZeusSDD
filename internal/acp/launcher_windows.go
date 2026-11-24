package acp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func launcherCommand(ctx context.Context, path string, env []string) (*exec.Cmd, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cmd", ".bat":
		// Expand the path once inside cmd.exe. Quoting and disabled delayed
		// expansion preserve spaces and cmd metacharacters in the filename.
		const variable = "ZEUSSDD_ACP_LAUNCHER"
		systemRoot := os.Getenv("SystemRoot")
		if !filepath.IsAbs(systemRoot) {
			return nil, fmt.Errorf("acp: SystemRoot must be an absolute path to launch %s", path)
		}
		command := exec.CommandContext(ctx, filepath.Join(systemRoot, "System32", "cmd.exe"))
		command.SysProcAttr = &syscall.SysProcAttr{
			CmdLine:    "cmd.exe /d /v:off /s /c \"\"%" + variable + "%\" --acp --approval-mode default\"",
			HideWindow: true,
		}
		command.Env = append(env, variable+"="+path)
		return command, nil
	default:
		return directCommand(ctx, path, env), nil
	}
}

func startLauncher(command *exec.Cmd) (func(), error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	dispose := func() { _ = windows.CloseHandle(job) }
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)))
	if err != nil {
		dispose()
		return nil, err
	}
	if err = command.Start(); err != nil {
		dispose()
		return nil, err
	}
	// The job owns any child started by the Windows launcher as well.
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(command.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, process)
		_ = windows.CloseHandle(process)
	}
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		dispose()
		return nil, fmt.Errorf("attach GigaCode to Windows job: %w", err)
	}
	return dispose, nil
}
