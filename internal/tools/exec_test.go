package tools

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func runExecCommandTool(t *testing.T, payload any) string {
	t.Helper()

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}

	tool := &ExecCommandTool{}
	out, err := tool.Call(context.Background(), json.RawMessage(data))
	if err != nil {
		t.Fatalf("exec_command returned error: %v", err)
	}
	return out
}

func TestExecCommandFallsBackToShellForInlineCommand(t *testing.T) {
	command := `echo "Hello"`
	if runtime.GOOS == "windows" {
		command = "echo Hello"
	}

	out := runExecCommandTool(t, map[string]any{"command": command})

	if !strings.Contains(out, "exit_code: 0") {
		t.Fatalf("expected successful exit code, got:\n%s", out)
	}
	if !strings.Contains(out, "execution_mode: shell") {
		t.Fatalf("expected shell execution mode, got:\n%s", out)
	}
	if !strings.Contains(out, "fallback_to_shell: true") {
		t.Fatalf("expected shell fallback to be true, got:\n%s", out)
	}
	if !strings.Contains(out, "stdout:\nHello") {
		t.Fatalf("expected stdout to contain Hello, got:\n%s", out)
	}
}

func TestExecCommandReportsCommandNotFoundDetails(t *testing.T) {
	out := runExecCommandTool(t, map[string]any{"command": "__definitely_missing_command__"})

	if !strings.Contains(out, "exit_code: -1") {
		t.Fatalf("expected exit_code -1, got:\n%s", out)
	}
	if !strings.Contains(out, "error_type: command_not_found") {
		t.Fatalf("expected command_not_found error type, got:\n%s", out)
	}
	if !strings.Contains(out, "error_message:") {
		t.Fatalf("expected error_message to be present, got:\n%s", out)
	}
	if strings.Contains(out, "error_message: -") {
		t.Fatalf("expected a non-empty error_message, got:\n%s", out)
	}
}

func TestExecCommandAcceptsCmdAlias(t *testing.T) {
	command := "echo alias_ok"

	out := runExecCommandTool(t, map[string]any{
		"cmd":       command,
		"use_shell": true,
	})

	if !strings.Contains(out, "exit_code: 0") {
		t.Fatalf("expected successful exit code, got:\n%s", out)
	}
	if !strings.Contains(out, "execution_mode: shell") {
		t.Fatalf("expected shell execution mode, got:\n%s", out)
	}
	if !strings.Contains(out, "command: "+command) {
		t.Fatalf("expected command field to show alias command, got:\n%s", out)
	}
	if !strings.Contains(out, "stdout:\nalias_ok") {
		t.Fatalf("expected stdout to contain alias_ok, got:\n%s", out)
	}
}
