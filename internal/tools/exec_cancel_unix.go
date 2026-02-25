//go:build !windows

package tools

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureExecCommandCancellation(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

	// Ensure context cancellation (including timeout) kills the whole process group,
	// not only the immediate child. This prevents hangs when orphaned subprocesses
	// keep stdout/stderr pipes open.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pid := cmd.Process.Pid
		if pid <= 0 {
			return nil
		}

		// Best-effort: kill the process group, then fall back to the direct process.
		// When Setpgid is set, the process group id is the child's pid.
		if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		return nil
	}
}
