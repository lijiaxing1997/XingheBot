package supervisor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
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

	// EnvDisableSupervisor disables auto-supervision.
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

	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, supervisorSignals()...)
	defer signal.Stop(sigCh)

	restarts := 0
	for {
		cmd := exec.Command(exe, args...)
		cmd.Env = childEnv
		cmd.Dir = cwd
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Start(); err != nil {
			return 1, err
		}
		waitCh := make(chan error, 1)
		go func() { waitCh <- cmd.Wait() }()

		shutdown := false
		killCh := (<-chan time.Time)(nil)
		var killTimer *time.Timer

		for {
			select {
			case sig := <-sigCh:
				shutdown = true
				if cmd.Process != nil {
					_ = cmd.Process.Signal(sig)
				}
				if killTimer == nil {
					killTimer = time.NewTimer(8 * time.Second)
					killCh = killTimer.C
				}
			case <-killCh:
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				killCh = nil
			case runErr := <-waitCh:
				if killTimer != nil {
					killTimer.Stop()
				}
				code, err := exitCode(runErr)
				if err != nil {
					return 1, err
				}
				if shutdown {
					return code, nil
				}
				if code != restart.ExitCodeRestartRequested {
					return code, nil
				}
				goto scheduleRestart
			}
		}

	scheduleRestart:
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
