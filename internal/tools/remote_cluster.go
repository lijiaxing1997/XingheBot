package tools

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"test_skill_agent/internal/cluster"
	"test_skill_agent/internal/llm"
)

type RemoteSlaveListTool struct {
	Registry *cluster.SlaveRegistry
}

type remoteSlaveListArgs struct {
	Query      string `json:"query"`
	OnlyOnline *bool  `json:"only_online"`
}

func (t *RemoteSlaveListTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "remote_slave_list",
			Description: "List online (or recently seen) slave nodes connected to the current master.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":       map[string]any{"type": "string"},
					"only_online": map[string]any{"type": "boolean"},
				},
			},
		},
	}
}

func (t *RemoteSlaveListTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Registry == nil {
		return "", errors.New("slave registry is not configured")
	}
	var in remoteSlaveListArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
	}
	onlyOnline := true
	if in.OnlyOnline != nil {
		onlyOnline = *in.OnlyOnline
	}
	query := strings.ToLower(strings.TrimSpace(in.Query))

	slaves := t.Registry.Snapshot(onlyOnline)
	if query != "" {
		filtered := make([]cluster.SlaveInfo, 0, len(slaves))
		for _, s := range slaves {
			if strings.Contains(strings.ToLower(s.SlaveID), query) || strings.Contains(strings.ToLower(s.Name), query) {
				filtered = append(filtered, s)
				continue
			}
			for k, v := range s.Meta {
				kv := strings.ToLower(strings.TrimSpace(k))
				if strings.Contains(kv, query) {
					filtered = append(filtered, s)
					break
				}
				if str, ok := v.(string); ok {
					if strings.Contains(strings.ToLower(str), query) {
						filtered = append(filtered, s)
						break
					}
				}
			}
		}
		slaves = filtered
	}

	sort.Slice(slaves, func(i, j int) bool {
		return slaves[i].LastSeen.After(slaves[j].LastSeen)
	})

	return prettyJSON(map[string]any{
		"count":      len(slaves),
		"only_online": onlyOnline,
		"checked_at": time.Now().UTC(),
		"slaves":     slaves,
	})
}

type RemoteAgentRunTool struct {
	Gateway *cluster.MasterGateway
}

type remoteAgentRunArgs struct {
	Slave   string              `json:"slave"`
	Task    string              `json:"task"`
	Options cluster.AgentRunOptions `json:"options"`
}

func (t *RemoteAgentRunTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "remote_agent_run",
			Description: "Run a task on a connected slave agent via WebSocket and return the result.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"slave": map[string]any{"type": "string"},
					"task":  map[string]any{"type": "string"},
					"options": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"max_turns":        map[string]any{"type": "integer"},
							"temperature":      map[string]any{"type": "number"},
							"max_tokens":       map[string]any{"type": "integer"},
							"timeout_seconds":  map[string]any{"type": "integer"},
						},
					},
				},
				"required": []string{"slave", "task"},
			},
		},
	}
}

func (t *RemoteAgentRunTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Gateway == nil {
		return "", errors.New("master gateway is not configured")
	}
	var in remoteAgentRunArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	slaveID := strings.TrimSpace(in.Slave)
	task := strings.TrimSpace(in.Task)
	if slaveID == "" || task == "" {
		return "", errors.New("slave and task are required")
	}

	timeout := 15 * time.Minute
	if in.Options.TimeoutSeconds > 0 {
		timeout = time.Duration(in.Options.TimeoutSeconds) * time.Second
	}
	payload := cluster.AgentRunPayload{
		Task:    task,
		Options: in.Options,
		Metadata: map[string]any{
			"source": "remote_agent_run",
		},
	}

	reqID, res, err := t.Gateway.SendAgentRun(ctx, slaveID, payload, timeout)
	if err != nil {
		status := "failed"
		if strings.Contains(strings.ToLower(err.Error()), "offline") {
			status = "offline"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			status = "timeout"
		}
		return prettyJSON(map[string]any{
			"slave_id":   slaveID,
			"request_id": reqID,
			"status":     status,
			"error":      err.Error(),
		})
	}

	return prettyJSON(map[string]any{
		"slave_id":   slaveID,
		"request_id": reqID,
		"status":     strings.TrimSpace(res.Status),
		"output":     res.Output,
		"error":      strings.TrimSpace(res.Error),
		"duration_ms": res.DurationMS,
		"run_id":     strings.TrimSpace(res.RunID),
	})
}

