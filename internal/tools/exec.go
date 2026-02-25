package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode"

	"test_skill_agent/internal/llm"
)

type ExecCommandTool struct{}

type execCommandArgs struct {
	Command        string            `json:"command"`
	Cmd            string            `json:"cmd"`
	Args           []string          `json:"args"`
	Dir            string            `json:"dir"`
	Env            map[string]string `json:"env"`
	InheritEnv     bool              `json:"inherit_env"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	UseShell       bool              `json:"use_shell"`
	MaxOutputBytes int               `json:"max_output_bytes"`
}

func (t *ExecCommandTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name: "exec_command",
			Description: "Execute a local command and return detailed execution output. " +
				"Supports `command` or `cmd` (alias).",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{"type": "string", "description": "Executable name, or a shell command when use_shell=true"},
					"cmd": map[string]interface{}{
						"type":        "string",
						"description": "Alias for command (backward compatibility)",
					},
					"args":             map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
					"dir":              map[string]interface{}{"type": "string"},
					"env":              map[string]interface{}{"type": "object"},
					"inherit_env":      map[string]interface{}{"type": "boolean"},
					"timeout_seconds":  map[string]interface{}{"type": "integer"},
					"use_shell":        map[string]interface{}{"type": "boolean", "description": "Run via shell (sh -c / cmd /C)"},
					"max_output_bytes": map[string]interface{}{"type": "integer", "description": "Max bytes captured per stream (default 65536)"},
				},
			},
		},
	}
}

func (t *ExecCommandTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	var in execCommandArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("exec_command: invalid JSON arguments: %w", err)
	}

	command := strings.TrimSpace(in.Command)
	if command == "" {
		command = strings.TrimSpace(in.Cmd)
	}
	if command == "" {
		return "", errors.New("command is required (or cmd alias)")
	}

	timeout := time.Duration(in.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	maxBytes := in.MaxOutputBytes
	if maxBytes <= 0 {
		maxBytes = 65536
	}

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	stdoutCapture := &limitedBuffer{buf: &stdout, max: maxBytes}
	stderrCapture := &limitedBuffer{buf: &stderr, max: maxBytes}

	buildCommand := func(useShell bool) *exec.Cmd {
		if useShell {
			if runtime.GOOS == "windows" {
				return exec.CommandContext(cmdCtx, "cmd", "/C", command)
			}
			return exec.CommandContext(cmdCtx, "sh", "-c", command)
		}
		return exec.CommandContext(cmdCtx, command, in.Args...)
	}

	prepareCommand := func(cmd *exec.Cmd) {
		if in.Dir != "" {
			cmd.Dir = in.Dir
		}
		if in.InheritEnv {
			cmd.Env = os.Environ()
		}
		if len(in.Env) > 0 {
			if cmd.Env == nil {
				cmd.Env = os.Environ()
			}
			for k, v := range in.Env {
				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
			}
		}
		cmd.Stdout = stdoutCapture
		cmd.Stderr = stderrCapture
		// Bound how long Run/Wait can hang after cancellation if orphaned subprocesses
		// keep stdout/stderr pipes open.
		cmd.WaitDelay = 500 * time.Millisecond
		configureExecCommandCancellation(cmd)
	}

	start := time.Now()
	usedShell := in.UseShell
	fallbackToShell := false

	cmd := buildCommand(usedShell)
	prepareCommand(cmd)
	err := cmd.Run()

	if shouldFallbackToShell(err, usedShell, in.Args, command) {
		fallbackToShell = true
		usedShell = true
		stdoutCapture.Reset()
		stderrCapture.Reset()

		cmd = buildCommand(true)
		prepareCommand(cmd)
		err = cmd.Run()
	}

	ctxErr := cmdCtx.Err()
	timedOut := errors.Is(ctxErr, context.DeadlineExceeded)
	canceled := errors.Is(ctxErr, context.Canceled)

	exitCode, errorType := classifyExecError(err)
	if err == nil {
		errorType = "none"
	} else if timedOut {
		exitCode = -1
		errorType = "timeout"
	} else if canceled {
		exitCode = -1
		errorType = "canceled"
	}
	errorMessage := "-"
	if err != nil {
		errorMessage = strings.TrimSpace(err.Error())
	}
	argsJSON, _ := json.Marshal(in.Args)

	out := fmt.Sprintf(
		"exit_code: %d\n"+
			"duration_ms: %d\n"+
			"timed_out: %t\n"+
			"execution_mode: %s\n"+
			"fallback_to_shell: %t\n"+
			"command: %s\n"+
			"args_json: %s\n"+
			"error_type: %s\n"+
			"error_message: %s\n"+
			"stdout_truncated_bytes: %d\n"+
			"stderr_truncated_bytes: %d\n"+
			"stdout:\n%s\n"+
			"stderr:\n%s",
		exitCode,
		time.Since(start).Milliseconds(),
		timedOut,
		map[bool]string{true: "shell", false: "direct"}[usedShell],
		fallbackToShell,
		command,
		string(argsJSON),
		errorType,
		errorMessage,
		stdoutCapture.TruncatedBytes(),
		stderrCapture.TruncatedBytes(),
		strings.TrimRight(stdout.String(), "\n"),
		strings.TrimRight(stderr.String(), "\n"),
	)
	return out, nil
}

type limitedBuffer struct {
	buf       *bytes.Buffer
	max       int
	truncated int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.max <= 0 {
		return l.buf.Write(p)
	}
	remaining := l.max - l.buf.Len()
	if remaining <= 0 {
		l.truncated += len(p)
		return len(p), nil
	}
	if len(p) > remaining {
		l.truncated += len(p) - remaining
		p = p[:remaining]
	}
	return l.buf.Write(p)
}

func (l *limitedBuffer) TruncatedBytes() int {
	return l.truncated
}

func (l *limitedBuffer) Reset() {
	l.buf.Reset()
	l.truncated = 0
}

func shouldFallbackToShell(err error, usedShell bool, args []string, command string) bool {
	if err == nil || usedShell || len(args) > 0 {
		return false
	}
	if !looksLikeShellCommand(command) {
		return false
	}
	return isCommandNotFoundError(err)
}

func looksLikeShellCommand(command string) bool {
	if strings.IndexFunc(command, unicode.IsSpace) >= 0 {
		return true
	}
	return strings.ContainsAny(command, "\"'`|&;<>()$*?[]{}")
}

func isCommandNotFoundError(err error) bool {
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		if errors.Is(execErr.Err, exec.ErrNotFound) {
			return true
		}
	}

	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		if errors.Is(pathErr.Err, os.ErrNotExist) {
			return true
		}
	}

	return errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist)
}

func classifyExecError(err error) (exitCode int, errorType string) {
	if err == nil {
		return 0, "none"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return -1, "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return -1, "canceled"
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), "non_zero_exit"
	}

	var execErr *exec.Error
	if errors.As(err, &execErr) {
		if errors.Is(execErr.Err, exec.ErrNotFound) {
			return -1, "command_not_found"
		}
		return -1, "exec_error"
	}

	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		if errors.Is(pathErr.Err, os.ErrNotExist) {
			return -1, "command_not_found"
		}
		return -1, "path_error"
	}

	return -1, "runtime_error"
}
