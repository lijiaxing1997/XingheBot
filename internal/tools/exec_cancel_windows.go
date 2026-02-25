//go:build windows

package tools

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
)

func configureExecCommandCancellation(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}

	// On Windows, fall back to taskkill to terminate the full process tree.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pid := cmd.Process.Pid
		if pid <= 0 {
			return nil
		}

		// /T: terminate child processes, /F: force.
		// Ignore errors here and fall back to Process.Kill below.
		_ = exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()

		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		return nil
	}
}
