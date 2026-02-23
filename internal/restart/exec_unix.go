//go:build !windows

package restart

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ExecReplacement replaces the current process image with a new instance of the
// same executable (or the provided executable path).
//
// This is preferred over spawning a child + exiting for interactive TUI/CLI
// flows because it preserves the foreground job/process group.
func ExecReplacement(executable string, args []string) error {
	exe := strings.TrimSpace(executable)
	if exe == "" {
		resolved, err := os.Executable()
		if err != nil {
			return err
		}
		exe = resolved
	}
	exe = filepath.Clean(exe)
	argv := append([]string{exe}, args...)
	env := os.Environ()
	return syscall.Exec(exe, argv, env)
}
