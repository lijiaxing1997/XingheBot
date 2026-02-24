package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"test_skill_agent/internal/cluster"
	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/multiagent"
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
		"count":       len(slaves),
		"only_online": onlyOnline,
		"checked_at":  time.Now().UTC(),
		"slaves":      slaves,
	})
}

type RemoteAgentRunTool struct {
	Gateway     *cluster.MasterGateway
	Coordinator *multiagent.Coordinator
}

type remoteAgentRunArgs struct {
	Slave   string                  `json:"slave"`
	Task    string                  `json:"task"`
	Options cluster.AgentRunOptions `json:"options"`
}

func (t *RemoteAgentRunTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name: "remote_agent_run",
			Description: "Run a task on a connected slave agent via WebSocket. " +
				"In interactive sessions this tool is NON-BLOCKING: it spawns a sub-agent to run remotely and returns immediately. " +
				"Use agent_wait/agent_result (or TAB in the TUI) to monitor and retrieve the final output. " +
				"Outside a session context it runs synchronously and returns the final result.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"slave": map[string]any{"type": "string"},
					"task":  map[string]any{"type": "string"},
					"options": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"max_turns":       map[string]any{"type": "integer"},
							"temperature":     map[string]any{"type": "number"},
							"max_tokens":      map[string]any{"type": "integer"},
							"timeout_seconds": map[string]any{"type": "integer"},
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

	if runID, ok := multiagent.SessionRunID(ctx); ok && t.Coordinator != nil {
		return t.callAsync(ctx, runID, args, slaveID, task, payload, timeout)
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
		"slave_id":    slaveID,
		"request_id":  reqID,
		"status":      strings.TrimSpace(res.Status),
		"output":      res.Output,
		"error":       strings.TrimSpace(res.Error),
		"duration_ms": res.DurationMS,
		"run_id":      strings.TrimSpace(res.RunID),
	})
}

func (t *RemoteAgentRunTool) callAsync(ctx context.Context, runID string, rawArgs json.RawMessage, slaveID string, task string, payload cluster.AgentRunPayload, timeout time.Duration) (string, error) {
	reqID, resCh, cleanup, err := t.Gateway.StartAgentRun(ctx, slaveID, payload)
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

	agentID, spec, state, err := createRemoteRunAgent(t.Coordinator, runID, slaveID, reqID, task)
	if err != nil {
		waitCtx := ctx
		cancel := func() {}
		if timeout > 0 {
			waitCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		defer cancel()
		defer cleanup()

		select {
		case res := <-resCh:
			return prettyJSON(map[string]any{
				"slave_id":    slaveID,
				"request_id":  reqID,
				"status":      strings.TrimSpace(res.Status),
				"output":      res.Output,
				"error":       strings.TrimSpace(res.Error),
				"duration_ms": res.DurationMS,
				"run_id":      strings.TrimSpace(res.RunID),
			})
		case <-waitCtx.Done():
			return prettyJSON(map[string]any{
				"slave_id":   slaveID,
				"request_id": reqID,
				"status":     "timeout",
				"error":      waitCtx.Err().Error(),
			})
		}
	}

	now := time.Now().UTC()
	state.Status = multiagent.StatusRunning
	state.PID = os.Getpid()
	state.StartedAt = now
	_ = t.Coordinator.UpdateAgentState(runID, state)
	_, _ = t.Coordinator.AppendEvent(runID, agentID, multiagent.AgentEvent{
		Type: "tool_call_started",
		Payload: map[string]any{
			"name":       "remote_agent_run",
			"args":       strings.TrimSpace(string(rawArgs)),
			"request_id": reqID,
			"slave_id":   slaveID,
		},
		CreatedAt: now,
	})

	go func() {
		defer cleanup()

		waitCtx := ctx
		cancel := func() {}
		if timeout > 0 {
			waitCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		defer cancel()

		start := time.Now()
		var (
			res     cluster.AgentResultPayload
			waitErr error
		)
		select {
		case res = <-resCh:
		case <-waitCtx.Done():
			waitErr = waitCtx.Err()
		}
		elapsed := time.Since(start)

		finished := time.Now().UTC()
		finalStatus := multiagent.StatusCompleted
		errText := ""
		output := ""
		if waitErr != nil {
			finalStatus = multiagent.StatusFailed
			errText = waitErr.Error()
		} else {
			output = strings.TrimSpace(res.Output)
			errText = strings.TrimSpace(res.Error)
			if !strings.EqualFold(strings.TrimSpace(res.Status), "completed") || errText != "" {
				finalStatus = multiagent.StatusFailed
				if errText == "" {
					errText = strings.TrimSpace(res.Status)
				}
			}
		}

		resultHeader := fmt.Sprintf(
			"remote_agent_run: slave_id=%s request_id=%s status=%s remote_run_id=%s duration_ms=%d\n",
			slaveID,
			reqID,
			func() string {
				if waitErr != nil {
					return "timeout"
				}
				if s := strings.TrimSpace(res.Status); s != "" {
					return s
				}
				return "unknown"
			}(),
			strings.TrimSpace(res.RunID),
			func() int64 {
				if res.DurationMS > 0 {
					return res.DurationMS
				}
				return elapsed.Milliseconds()
			}(),
		)
		finalOutput := resultHeader
		if output != "" {
			finalOutput += "\n" + output
		}

		resultPath := t.Coordinator.AgentResultPath(runID, agentID)
		_ = t.Coordinator.WriteResult(runID, agentID, multiagent.AgentResult{
			RunID:      runID,
			AgentID:    agentID,
			Status:     finalStatus,
			Output:     finalOutput,
			Error:      errText,
			FinishedAt: finished,
		})

		if state, err := t.Coordinator.ReadAgentState(runID, agentID); err == nil {
			state.Status = finalStatus
			state.Error = errText
			state.ResultPath = resultPath
			_ = t.Coordinator.UpdateAgentState(runID, state)
		} else {
			_ = t.Coordinator.UpdateAgentState(runID, multiagent.AgentState{
				RunID:      runID,
				AgentID:    agentID,
				Status:     finalStatus,
				PID:        os.Getpid(),
				CreatedAt:  spec.CreatedAt,
				StartedAt:  state.StartedAt,
				FinishedAt: finished,
				UpdatedAt:  finished,
				ResultPath: resultPath,
				Error:      errText,
			})
		}

		preview := output
		if preview == "" && errText != "" {
			preview = errText
		}
		preview = truncateString(preview, 5000)
		toolResult, _ := prettyJSON(map[string]any{
			"slave_id":       slaveID,
			"request_id":     reqID,
			"status":         strings.TrimSpace(res.Status),
			"remote_run_id":  strings.TrimSpace(res.RunID),
			"duration_ms":    res.DurationMS,
			"output_preview": preview,
			"error":          strings.TrimSpace(res.Error),
		})
		_, _ = t.Coordinator.AppendEvent(runID, agentID, multiagent.AgentEvent{
			Type: "tool_call_finished",
			Payload: map[string]any{
				"name":        "remote_agent_run",
				"status":      strings.ToLower(strings.TrimSpace(finalStatus)),
				"error":       errText,
				"result":      toolResult,
				"duration_ms": float64(elapsed.Milliseconds()),
			},
			CreatedAt: finished,
		})
	}()

	return prettyJSON(map[string]any{
		"slave_id":    slaveID,
		"request_id":  reqID,
		"status":      "started",
		"run_id":      runID,
		"agent_id":    agentID,
		"agent_task":  spec.Task,
		"timeout_sec": int(timeout.Seconds()),
	})
}

func createRemoteRunAgent(coord *multiagent.Coordinator, runID string, slaveID string, requestID string, task string) (string, multiagent.AgentSpec, multiagent.AgentState, error) {
	if coord == nil {
		return "", multiagent.AgentSpec{}, multiagent.AgentState{}, errors.New("multi-agent coordinator is not configured")
	}
	if strings.TrimSpace(runID) == "" {
		return "", multiagent.AgentSpec{}, multiagent.AgentState{}, errors.New("run_id is required")
	}

	taskPreview := strings.TrimSpace(task)
	taskPreview = truncateString(taskPreview, 160)
	specTask := fmt.Sprintf("remote_agent_run slave_id=%s request_id=%s task=%s", slaveID, requestID, taskPreview)

	for attempt := 0; attempt < 4; attempt++ {
		agentID := newRemoteAgentID()
		spec, state, err := coord.CreateAgent(runID, multiagent.AgentSpec{
			ID:   agentID,
			Task: specTask,
			Metadata: map[string]any{
				"type":       "remote_agent_run",
				"slave_id":   slaveID,
				"request_id": requestID,
			},
		})
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "already exists") {
				continue
			}
			return "", multiagent.AgentSpec{}, multiagent.AgentState{}, err
		}
		return spec.ID, spec, state, nil
	}
	return "", multiagent.AgentSpec{}, multiagent.AgentState{}, errors.New("failed to allocate unique agent_id for remote run")
}

func newRemoteAgentID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("remote-%d", time.Now().UTC().UnixNano())
	}
	return "remote-" + hex.EncodeToString(b[:])
}

func truncateString(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	if max < 16 {
		return s[:max]
	}
	return s[:max-14] + "\n... (truncated)"
}
