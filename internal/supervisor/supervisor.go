package supervisor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"test_skill_agent/internal/restart"
)

const (
	// EnvSupervised is set in the child process environment when launched by a
	// foreground supervisor.
	EnvSupervised = "XINGHEBOT_SUPERVISED"

	// EnvSupervisorPID is informational only (helps debugging).
	EnvSupervisorPID = "XINGHEBOT_SUPERVISOR_PID"

	// EnvDisableSupervisor disables auto-supervision for interactive chat.
	EnvDisableSupervisor = "XINGHEBOT_NO_SUPERVISOR"
)

func IsSupervisedChild() bool {
	return strings.TrimSpace(os.Getenv(EnvSupervised)) != ""
}

func SupervisorDisabled() bool {
	v := strings.TrimSpace(os.Getenv(EnvDisableSupervisor))
	if v == "" {
		return false
	}
	switch strings.ToLower(v) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func RunForegroundLoop(args []string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 1, err
	}
	cwd, _ := os.Getwd()

	baseEnv := os.Environ()
	childEnv := append([]string{}, baseEnv...)
	childEnv = append(childEnv,
		EnvSupervised+"=1",
		EnvSupervisorPID+"="+strconv.Itoa(os.Getpid()),
	)

	restarts := 0
	for {
		cmd := exec.Command(exe, args...)
		cmd.Env = childEnv
		cmd.Dir = cwd
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		runErr := cmd.Run()
		code, err := exitCode(runErr)
		if err != nil {
			return 1, err
		}

		if code != restart.ExitCodeRestartRequested {
			return code, nil
		}

		restarts++
		if restarts > 25 {
			return 1, fmt.Errorf("too many restarts (%d)", restarts)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func exitCode(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 1, err
}
