package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"test_skill_agent/internal/autonomy"
	"test_skill_agent/internal/autonomy/heartbeatrunner"
	"test_skill_agent/internal/llm"
)

type HeartbeatStatusTool struct {
	ConfigPath string
	Runner     *heartbeatrunner.Runner
}

func (t *HeartbeatStatusTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "heartbeat_status",
			Description: "Show heartbeat (autonomy) status, including config, HEARTBEAT.md path, and runner state (if running).",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{
					// No args for now.
				},
			},
		},
	}
}

func (t *HeartbeatStatusTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	_ = args
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cfg, err := autonomy.LoadConfig(t.ConfigPath)
	if err != nil {
		return "", err
	}
	wd, _ := os.Getwd()
	hbPath, _ := heartbeatrunner.ResolveHeartbeatFilePath(cfg.Heartbeat.Path, wd)
	content, exists, effectivelyEmpty, readErr := heartbeatrunner.ReadHeartbeatFile(hbPath)
	sizeBytes := 0
	if exists {
		sizeBytes = len([]byte(content))
	}

	var runnerStatus any = nil
	if t.Runner != nil {
		if st, err := t.Runner.Status(); err == nil {
			runnerStatus = st
		}
	}

	payload := map[string]any{
		"autonomy": cfg.WithDefaults(),
		"heartbeat_file": map[string]any{
			"path":              hbPath,
			"exists":            exists,
			"size_bytes":        sizeBytes,
			"effectively_empty": effectivelyEmpty,
			"read_error":        errString(readErr),
		},
		"runner": runnerStatus,
	}
	return prettyJSON(payload)
}

type HeartbeatGetTool struct {
	ConfigPath string
}

func (t *HeartbeatGetTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "heartbeat_get",
			Description: "Read HEARTBEAT.md (autonomy heartbeat instructions).",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{
					// No args for now.
				},
			},
		},
	}
}

func (t *HeartbeatGetTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	_ = args
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cfg, err := autonomy.LoadConfig(t.ConfigPath)
	if err != nil {
		return "", err
	}
	wd, _ := os.Getwd()
	hbPath, _ := heartbeatrunner.ResolveHeartbeatFilePath(cfg.Heartbeat.Path, wd)
	content, exists, effectivelyEmpty, err := heartbeatrunner.ReadHeartbeatFile(hbPath)
	if err != nil {
		return "", err
	}
	out := map[string]any{
		"path":              hbPath,
		"exists":            exists,
		"effectively_empty": effectivelyEmpty,
		"content":           content,
	}
	return prettyJSON(out)
}

type HeartbeatSetTool struct {
	ConfigPath string
	Wake       func(reason string)
}

type heartbeatSetArgs struct {
	Content string `json:"content"`
	Mode    string `json:"mode"` // replace|append
}

func (t *HeartbeatSetTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "heartbeat_set",
			Description: "Write HEARTBEAT.md content (replace by default, or append).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"content": map[string]any{"type": "string", "description": "New HEARTBEAT.md content."},
					"mode":    map[string]any{"type": "string", "description": "replace|append (default: replace)"},
				},
				"required": []string{"content"},
			},
		},
	}
}

func (t *HeartbeatSetTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var in heartbeatSetArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Content) == "" {
		return "", errors.New("content is required")
	}

	cfg, err := autonomy.LoadConfig(t.ConfigPath)
	if err != nil {
		return "", err
	}
	wd, _ := os.Getwd()
	hbPath, _ := heartbeatrunner.ResolveHeartbeatFilePath(cfg.Heartbeat.Path, wd)

	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	content := in.Content
	if mode == "append" {
		old, _, _, _ := heartbeatrunner.ReadHeartbeatFile(hbPath)
		if strings.TrimSpace(old) != "" {
			content = strings.TrimRight(old, "\n") + "\n\n" + strings.TrimLeft(in.Content, "\n")
		}
	}
	if err := heartbeatrunner.WriteHeartbeatFileAtomic(hbPath, content); err != nil {
		return "", err
	}
	if t.Wake != nil {
		t.Wake("wake")
	}
	return prettyJSON(map[string]any{
		"status": "ok",
		"path":   hbPath,
	})
}

type HeartbeatClearTool struct {
	ConfigPath string
	Wake       func(reason string)
}

func (t *HeartbeatClearTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "heartbeat_clear",
			Description: "Clear HEARTBEAT.md to an effectively-empty template (so interval heartbeat skips).",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{
					// No args.
				},
			},
		},
	}
}

func (t *HeartbeatClearTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	_ = args
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cfg, err := autonomy.LoadConfig(t.ConfigPath)
	if err != nil {
		return "", err
	}
	wd, _ := os.Getwd()
	hbPath, _ := heartbeatrunner.ResolveHeartbeatFilePath(cfg.Heartbeat.Path, wd)

	const emptyTemplate = "# HEARTBEAT.md\n\n- [ ]\n"
	if err := heartbeatrunner.WriteHeartbeatFileAtomic(hbPath, emptyTemplate); err != nil {
		return "", err
	}
	if t.Wake != nil {
		t.Wake("wake")
	}
	return prettyJSON(map[string]any{
		"status": "ok",
		"path":   hbPath,
	})
}

type HeartbeatRunNowTool struct {
	Wake func(reason string)
}

func (t *HeartbeatRunNowTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "heartbeat_run_now",
			Description: "Trigger an immediate heartbeat run (async).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{"type": "string", "description": "Optional reason label."},
				},
			},
		},
	}
}

func (t *HeartbeatRunNowTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if t.Wake == nil {
		return "", errors.New("heartbeat runner is not running")
	}
	var in struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(args, &in)
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "manual"
	}
	t.Wake(reason)
	return prettyJSON(map[string]any{
		"status": "ok",
		"queued": true,
		"reason": reason,
	})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
