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

    "test_skill_agent/internal/llm"
)

type ExecCommandTool struct{}

type execCommandArgs struct {
    Command         string            `json:"command"`
    Args            []string          `json:"args"`
    Dir             string            `json:"dir"`
    Env             map[string]string `json:"env"`
    InheritEnv      bool              `json:"inherit_env"`
    TimeoutSeconds  int               `json:"timeout_seconds"`
    UseShell        bool              `json:"use_shell"`
    MaxOutputBytes  int               `json:"max_output_bytes"`
}

func (t *ExecCommandTool) Definition() llm.ToolDefinition {
    return llm.ToolDefinition{
        Type: "function",
        Function: llm.ToolFunctionDef{
            Name:        "exec_command",
            Description: "Execute a local command and return stdout/stderr.",
            Parameters: map[string]interface{}{
                "type": "object",
                "properties": map[string]interface{}{
                    "command": map[string]interface{}{"type": "string", "description": "Executable or shell command"},
                    "args": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
                    "dir": map[string]interface{}{"type": "string"},
                    "env": map[string]interface{}{"type": "object"},
                    "inherit_env": map[string]interface{}{"type": "boolean"},
                    "timeout_seconds": map[string]interface{}{"type": "integer"},
                    "use_shell": map[string]interface{}{"type": "boolean", "description": "Run via shell (sh -c / cmd /C)"},
                    "max_output_bytes": map[string]interface{}{"type": "integer", "description": "Max combined output bytes (default 65536)"},
                },
                "required": []string{"command"},
            },
        },
    }
}

func (t *ExecCommandTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
    var in execCommandArgs
    if err := json.Unmarshal(args, &in); err != nil {
        return "", err
    }
    if strings.TrimSpace(in.Command) == "" {
        return "", errors.New("command is required")
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

    var cmd *exec.Cmd
    if in.UseShell {
        if runtime.GOOS == "windows" {
            cmd = exec.CommandContext(cmdCtx, "cmd", "/C", in.Command)
        } else {
            cmd = exec.CommandContext(cmdCtx, "sh", "-c", in.Command)
        }
    } else {
        cmd = exec.CommandContext(cmdCtx, in.Command, in.Args...)
    }

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

    var stdout bytes.Buffer
    var stderr bytes.Buffer
    cmd.Stdout = &limitedBuffer{buf: &stdout, max: maxBytes}
    cmd.Stderr = &limitedBuffer{buf: &stderr, max: maxBytes}

    err := cmd.Run()
    exitCode := 0
    if err != nil {
        if exitErr, ok := err.(*exec.ExitError); ok {
            exitCode = exitErr.ExitCode()
        } else if errors.Is(err, context.DeadlineExceeded) {
            exitCode = -1
        } else {
            exitCode = -1
        }
    }

    out := fmt.Sprintf("exit_code: %d\nstdout:\n%s\nstderr:\n%s", exitCode, strings.TrimRight(stdout.String(), "\n"), strings.TrimRight(stderr.String(), "\n"))
    if err != nil {
        return out, nil
    }
    return out, nil
}

type limitedBuffer struct {
    buf *bytes.Buffer
    max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
    if l.max <= 0 {
        return l.buf.Write(p)
    }
    remaining := l.max - l.buf.Len()
    if remaining <= 0 {
        return len(p), nil
    }
    if len(p) > remaining {
        p = p[:remaining]
    }
    return l.buf.Write(p)
}
