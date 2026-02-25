//go:build !windows

package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestExecCommandTimeoutKillsProcessTree(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child_pid.txt")
	quotedPidFile := shellQuote(pidFile)

	// Spawn a background child that inherits stdout/stderr, then wait.
	// If we only kill the immediate shell process, the orphaned child can keep
	// stdout/stderr pipes open and cause Run/Wait to hang past the context timeout.
	command := fmt.Sprintf("sleep 10 & echo $! > %s; wait", quotedPidFile)

	start := time.Now()
	out := runExecCommandTool(t, map[string]any{
		"command":         command,
		"use_shell":       true,
		"timeout_seconds": 1,
	})
	elapsed := time.Since(start)

	if elapsed > 4*time.Second {
		t.Fatalf("expected exec_command to return shortly after timeout, elapsed=%v, out:\n%s", elapsed, out)
	}
	if !strings.Contains(out, "timed_out: true") {
		t.Fatalf("expected timed_out true, got:\n%s", out)
	}

	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("expected pid file to exist: %v, out:\n%s", err, out)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil || pid <= 0 {
		t.Fatalf("invalid pid %q: %v, out:\n%s", strings.TrimSpace(string(pidBytes)), err, out)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		killErr := syscall.Kill(pid, 0)
		if killErr != nil && errors.Is(killErr, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected child pid %d to be terminated, still running, out:\n%s", pid, out)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
