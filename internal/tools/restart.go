package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"test_skill_agent/internal/appinfo"
	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/restart"
)

type AgentRestartTool struct {
	Manager *restart.Manager
}

type agentRestartArgs struct {
	RunID   string `json:"run_id"`
	Reason  string `json:"reason"`
	Note    string `json:"note"`
	Message string `json:"message"`
}

func (t *AgentRestartTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "agent_restart",
			Description: "Request the XingheBot process to restart (self-respawn). Writes a restart sentinel for UI display after restart.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":  map[string]any{"type": "string", "description": "Optional run_id context (auto-injected in TUI)."},
					"reason":  map[string]any{"type": "string"},
					"note":    map[string]any{"type": "string"},
					"message": map[string]any{"type": "string", "description": "Alias for note."},
				},
			},
		},
	}
}

func (t *AgentRestartTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Manager == nil {
		return "", errors.New("agent_restart: restart manager is not configured")
	}
	var in agentRestartArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
	}

	reason := strings.TrimSpace(in.Reason)
	note := strings.TrimSpace(in.Note)
	if note == "" {
		note = strings.TrimSpace(in.Message)
	}
	runID := strings.TrimSpace(in.RunID)

	path, accepted, err := t.Manager.RequestRestart(restart.SentinelEntry{
		Kind:    "restart",
		Status:  "ok",
		TS:      time.Now().UTC(),
		App:     appinfo.Name,
		Version: appinfo.Version,
		RunID:   runID,
		Reason:  reason,
		Note:    note,
	})
	if err != nil {
		return "", err
	}

	return prettyJSON(map[string]any{
		"ok":            true,
		"requested":     accepted,
		"sentinel_path": path,
		"app":           appinfo.Name,
		"version":       appinfo.Version,
		"run_id":        runID,
		"reason":        reason,
		"note":          note,
	})
}
