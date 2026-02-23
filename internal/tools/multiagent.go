package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/multiagent"
)

type AgentRunCreateTool struct {
	Coordinator *multiagent.Coordinator
}

type agentRunCreateArgs struct {
	RunID    string         `json:"run_id"`
	Metadata map[string]any `json:"metadata"`
}

func (t *AgentRunCreateTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "agent_run_create",
			Description: "Create (or reuse) a multi-agent run directory for coordinating child agents via JSON files.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":   map[string]any{"type": "string"},
					"metadata": map[string]any{"type": "object"},
				},
			},
		},
	}
}

func (t *AgentRunCreateTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Coordinator == nil {
		return "", errors.New("multi-agent coordinator is not configured")
	}
	var in agentRunCreateArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
	}
	run, err := t.Coordinator.CreateRun(in.RunID, in.Metadata)
	if err != nil {
		return "", err
	}
	return prettyJSON(map[string]any{
		"run_id":           run.ID,
		"run_dir":          t.Coordinator.RunDir(run.ID),
		"run_manifest":     t.Coordinator.RunManifestPath(run.ID),
		"created_at":       run.CreatedAt,
		"signals_dir":      t.Coordinator.SignalsDir(run.ID),
		"agents_dir":       t.Coordinator.AgentsDir(run.ID),
		"coordinator_root": t.Coordinator.RunRoot,
	})
}

type AgentSpawnTool struct {
	Coordinator        *multiagent.Coordinator
	Executable         string
	SkillsDir          string
	ConfigPath         string
	MCPConfigPath      string
	DefaultTemperature float64
	DefaultMaxTokens   int
	WorkDir            string
}

type agentSpawnArgs struct {
	RunID       string         `json:"run_id"`
	AgentID     string         `json:"agent_id"`
	Task        string         `json:"task"`
	MaxTurns    int            `json:"max_turns"`
	Temperature *float64       `json:"temperature"`
	MaxTokens   int            `json:"max_tokens"`
	Metadata    map[string]any `json:"metadata"`
	WorkDir     string         `json:"work_dir"`
}

func (t *AgentSpawnTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name: "agent_spawn",
			Description: "Spawn a child agent process that executes a task with full tool permissions. " +
				"Child progress/state/commands/events are synchronized through JSON files.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":      map[string]any{"type": "string"},
					"agent_id":    map[string]any{"type": "string"},
					"task":        map[string]any{"type": "string"},
					"max_turns":   map[string]any{"type": "integer"},
					"temperature": map[string]any{"type": "number"},
					"max_tokens":  map[string]any{"type": "integer"},
					"metadata":    map[string]any{"type": "object"},
					"work_dir":    map[string]any{"type": "string"},
				},
				"required": []string{"run_id", "task"},
			},
		},
	}
}

func (t *AgentSpawnTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Coordinator == nil {
		return "", errors.New("multi-agent coordinator is not configured")
	}
	var in agentSpawnArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.RunID) == "" {
		return "", errors.New("run_id is required")
	}
	if strings.TrimSpace(in.Task) == "" {
		return "", errors.New("task is required")
	}

	spec := multiagent.AgentSpec{
		ID:       in.AgentID,
		Task:     in.Task,
		MaxTurns: in.MaxTurns,
		Metadata: in.Metadata,
	}
	if in.Temperature != nil {
		temp := *in.Temperature
		spec.Temperature = &temp
	}
	if in.MaxTokens > 0 {
		spec.MaxTokens = in.MaxTokens
	}

	spec, state, err := t.Coordinator.CreateAgent(in.RunID, spec)
	if err != nil {
		return "", err
	}

	executable := strings.TrimSpace(t.Executable)
	if executable == "" {
		executable, err = os.Executable()
		if err != nil {
			return "", err
		}
	}
	temperature := t.DefaultTemperature
	if in.Temperature != nil {
		temperature = *in.Temperature
	}
	maxTokens := t.DefaultMaxTokens
	if in.MaxTokens > 0 {
		maxTokens = in.MaxTokens
	}
	workDir := strings.TrimSpace(in.WorkDir)
	if workDir == "" {
		workDir = strings.TrimSpace(t.WorkDir)
	}

	cmdArgs := []string{
		"worker",
		"--run-root", t.Coordinator.RunRoot,
		"--run-id", spec.RunID,
		"--agent-id", spec.ID,
		"--skills-dir", t.SkillsDir,
		"--temperature", strconv.FormatFloat(temperature, 'f', -1, 64),
		"--config", t.ConfigPath,
		"--mcp-config", t.MCPConfigPath,
	}
	if maxTokens > 0 {
		cmdArgs = append(cmdArgs, "--max-tokens", strconv.Itoa(maxTokens))
	}

	agentDir := t.Coordinator.AgentDir(spec.RunID, spec.ID)
	assetDir := filepath.Join(agentDir, "asset")
	_ = os.MkdirAll(assetDir, 0o755)
	stdoutPath := filepath.Join(agentDir, "stdout.log")
	stderrPath := filepath.Join(agentDir, "stderr.log")
	stdoutFile, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", err
	}
	defer stdoutFile.Close()
	stderrFile, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", err
	}
	defer stderrFile.Close()

	cmd := exec.Command(executable, cmdArgs...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Env = os.Environ()
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile

	if err := cmd.Start(); err != nil {
		return "", err
	}

	go func(runID string, agentID string) {
		waitErr := cmd.Wait()
		if waitErr == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
		state, err := t.Coordinator.ReadAgentState(runID, agentID)
		if err != nil {
			return
		}
		if multiagent.IsTerminalStatus(state.Status) {
			return
		}
		now := time.Now().UTC()
		state.Status = multiagent.StatusFailed
		state.Error = "worker process exited: " + waitErr.Error()
		state.FinishedAt = now
		state.UpdatedAt = now
		_ = t.Coordinator.UpdateAgentState(runID, state)
		_, _ = t.Coordinator.AppendEvent(runID, agentID, multiagent.AgentEvent{
			Type:      "process_exit",
			Message:   "worker process exited unexpectedly",
			CreatedAt: now,
			Payload: map[string]any{
				"error": waitErr.Error(),
			},
		})
	}(spec.RunID, spec.ID)

	now := time.Now().UTC()
	state.Status = multiagent.StatusRunning
	state.PID = cmd.Process.Pid
	if state.StartedAt.IsZero() {
		state.StartedAt = now
	}
	state.UpdatedAt = now
	if err := t.Coordinator.UpdateAgentState(spec.RunID, state); err != nil {
		return "", err
	}
	_, _ = t.Coordinator.AppendEvent(spec.RunID, spec.ID, multiagent.AgentEvent{
		Type:      "spawned",
		Message:   "child process spawned",
		CreatedAt: now,
		Payload: map[string]any{
			"pid":        cmd.Process.Pid,
			"executable": executable,
			"args":       strings.Join(cmdArgs, " "),
			"work_dir":   workDir,
		},
	})

	return prettyJSON(map[string]any{
		"run_id":      spec.RunID,
		"agent_id":    spec.ID,
		"pid":         cmd.Process.Pid,
		"status":      state.Status,
		"agent_dir":   agentDir,
		"asset_dir":   assetDir,
		"spec_path":   t.Coordinator.AgentSpecPath(spec.RunID, spec.ID),
		"state_path":  t.Coordinator.AgentStatePath(spec.RunID, spec.ID),
		"events_path": t.Coordinator.AgentEventsPath(spec.RunID, spec.ID),
		"stdout_log":  stdoutPath,
		"stderr_log":  stderrPath,
	})
}

type AgentStateTool struct {
	Coordinator *multiagent.Coordinator
}

type agentStateArgs struct {
	RunID   string `json:"run_id"`
	AgentID string `json:"agent_id"`
}

func (t *AgentStateTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "agent_state",
			Description: "Read child-agent state from JSON files. When agent_id is empty, return all states under a run.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":   map[string]any{"type": "string"},
					"agent_id": map[string]any{"type": "string"},
				},
				"required": []string{"run_id"},
			},
		},
	}
}

func (t *AgentStateTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Coordinator == nil {
		return "", errors.New("multi-agent coordinator is not configured")
	}
	var in agentStateArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.RunID) == "" {
		return "", errors.New("run_id is required")
	}
	if strings.TrimSpace(in.AgentID) != "" {
		state, err := t.Coordinator.ReadAgentState(in.RunID, in.AgentID)
		if err != nil {
			return "", err
		}
		return prettyJSON(state)
	}
	states, err := t.Coordinator.ListAgentStates(in.RunID)
	if err != nil {
		return "", err
	}
	return prettyJSON(map[string]any{
		"run_id": in.RunID,
		"count":  len(states),
		"states": states,
	})
}

type AgentWaitTool struct {
	Coordinator *multiagent.Coordinator
}

type agentWaitArgs struct {
	RunID          string `json:"run_id"`
	AgentID        string `json:"agent_id"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	PollMs         int    `json:"poll_ms"`
}

func (t *AgentWaitTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "agent_wait",
			Description: "Block until child agent(s) reach terminal states (completed/failed/canceled).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":          map[string]any{"type": "string"},
					"agent_id":        map[string]any{"type": "string"},
					"timeout_seconds": map[string]any{"type": "integer"},
					"poll_ms":         map[string]any{"type": "integer"},
				},
				"required": []string{"run_id"},
			},
		},
	}
}

func (t *AgentWaitTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Coordinator == nil {
		return "", errors.New("multi-agent coordinator is not configured")
	}
	var in agentWaitArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.RunID) == "" {
		return "", errors.New("run_id is required")
	}

	poll := 300 * time.Millisecond
	if in.PollMs > 0 {
		poll = time.Duration(in.PollMs) * time.Millisecond
	}
	var deadline time.Time
	if in.TimeoutSeconds > 0 {
		deadline = time.Now().UTC().Add(time.Duration(in.TimeoutSeconds) * time.Second)
	}

	for {
		targetStates, err := t.loadTargetStates(in.RunID, in.AgentID)
		if err != nil {
			return "", err
		}
		if len(targetStates) == 0 {
			return "", errors.New("no target agents found")
		}

		allDone := true
		for _, state := range targetStates {
			if !multiagent.IsTerminalStatus(state.Status) {
				allDone = false
				break
			}
		}
		if allDone {
			return prettyJSON(map[string]any{
				"run_id":     in.RunID,
				"agent_id":   in.AgentID,
				"timed_out":  false,
				"states":     targetStates,
				"checked_at": time.Now().UTC(),
			})
		}
		if !deadline.IsZero() && time.Now().UTC().After(deadline) {
			return prettyJSON(map[string]any{
				"run_id":     in.RunID,
				"agent_id":   in.AgentID,
				"timed_out":  true,
				"states":     targetStates,
				"checked_at": time.Now().UTC(),
			})
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(poll):
		}
	}
}

func (t *AgentWaitTool) loadTargetStates(runID string, agentID string) ([]multiagent.AgentState, error) {
	if strings.TrimSpace(agentID) != "" {
		state, err := t.Coordinator.ReadAgentState(runID, agentID)
		if err != nil {
			return nil, err
		}
		return []multiagent.AgentState{state}, nil
	}
	states, err := t.Coordinator.ListAgentStates(runID)
	if err != nil {
		return nil, err
	}
	sort.Slice(states, func(i, j int) bool {
		return states[i].AgentID < states[j].AgentID
	})
	return states, nil
}

type AgentControlTool struct {
	Coordinator        *multiagent.Coordinator
	Executable         string
	SkillsDir          string
	ConfigPath         string
	MCPConfigPath      string
	DefaultTemperature float64
	DefaultMaxTokens   int
	WorkDir            string
}

type agentControlArgs struct {
	RunID   string         `json:"run_id"`
	AgentID string         `json:"agent_id"`
	Command string         `json:"command"`
	Payload map[string]any `json:"payload"`
}

func (t *AgentControlTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "agent_control",
			Description: "Send a control command to a child agent. Commands: pause, resume, cancel, message.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":   map[string]any{"type": "string"},
					"agent_id": map[string]any{"type": "string"},
					"command": map[string]any{
						"type": "string",
						"enum": []string{"pause", "resume", "cancel", "message"},
					},
					"payload": map[string]any{"type": "object"},
				},
				"required": []string{"run_id", "agent_id", "command"},
			},
		},
	}
}

func (t *AgentControlTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Coordinator == nil {
		return "", errors.New("multi-agent coordinator is not configured")
	}
	var in agentControlArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.RunID) == "" || strings.TrimSpace(in.AgentID) == "" {
		return "", errors.New("run_id and agent_id are required")
	}
	command := strings.ToLower(strings.TrimSpace(in.Command))
	if command == "" {
		return "", errors.New("command is required")
	}
	cmd, err := t.Coordinator.AppendCommand(in.RunID, in.AgentID, multiagent.AgentCommand{
		Type:      command,
		Payload:   in.Payload,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return "", err
	}

	out := map[string]any{
		"seq":        cmd.Seq,
		"type":       cmd.Type,
		"payload":    cmd.Payload,
		"created_at": cmd.CreatedAt,
	}

	if command == multiagent.CommandMessage {
		revive := map[string]any{
			"attempted": false,
			"spawned":   false,
		}
		state, stateErr := t.Coordinator.ReadAgentState(in.RunID, in.AgentID)
		if stateErr == nil && multiagent.IsTerminalStatus(state.Status) {
			revive["attempted"] = true
			pid, reviveErr := t.reviveAgent(in.RunID, in.AgentID)
			if reviveErr != nil {
				revive["error"] = reviveErr.Error()
			} else if pid > 0 {
				revive["spawned"] = true
				revive["pid"] = pid
			}
		}
		out["revive"] = revive
	}

	return prettyJSON(out)
}

func (t *AgentControlTool) reviveAgent(runID string, agentID string) (int, error) {
	if t == nil || t.Coordinator == nil {
		return 0, errors.New("multi-agent coordinator is not configured")
	}
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(agentID) == "" {
		return 0, errors.New("run_id and agent_id are required")
	}
	if _, err := t.Coordinator.ReadAgentSpec(runID, agentID); err != nil {
		return 0, fmt.Errorf("agent spec not found: %w", err)
	}

	executable := strings.TrimSpace(t.Executable)
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return 0, err
		}
	}

	cmdArgs := []string{
		"worker",
		"--run-root", t.Coordinator.RunRoot,
		"--run-id", strings.TrimSpace(runID),
		"--agent-id", strings.TrimSpace(agentID),
		"--skills-dir", t.SkillsDir,
		"--temperature", strconv.FormatFloat(t.DefaultTemperature, 'f', -1, 64),
		"--config", t.ConfigPath,
		"--mcp-config", t.MCPConfigPath,
	}
	if t.DefaultMaxTokens > 0 {
		cmdArgs = append(cmdArgs, "--max-tokens", strconv.Itoa(t.DefaultMaxTokens))
	}

	agentDir := t.Coordinator.AgentDir(runID, agentID)
	stdoutPath := filepath.Join(agentDir, "stdout.log")
	stderrPath := filepath.Join(agentDir, "stderr.log")
	stdoutFile, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	defer stdoutFile.Close()
	stderrFile, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	defer stderrFile.Close()

	cmd := exec.Command(executable, cmdArgs...)
	workDir := strings.TrimSpace(t.WorkDir)
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Env = os.Environ()
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile

	if err := cmd.Start(); err != nil {
		return 0, err
	}

	go func(runID string, agentID string) {
		waitErr := cmd.Wait()
		if waitErr == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
		state, err := t.Coordinator.ReadAgentState(runID, agentID)
		if err != nil {
			return
		}
		if multiagent.IsTerminalStatus(state.Status) {
			return
		}
		now := time.Now().UTC()
		state.Status = multiagent.StatusFailed
		state.Error = "worker process exited: " + waitErr.Error()
		state.FinishedAt = now
		state.UpdatedAt = now
		_ = t.Coordinator.UpdateAgentState(runID, state)
		_, _ = t.Coordinator.AppendEvent(runID, agentID, multiagent.AgentEvent{
			Type:      "process_exit",
			Message:   "worker process exited unexpectedly",
			CreatedAt: now,
			Payload: map[string]any{
				"error": waitErr.Error(),
			},
		})
	}(strings.TrimSpace(runID), strings.TrimSpace(agentID))

	now := time.Now().UTC()
	state, err := t.Coordinator.ReadAgentState(runID, agentID)
	if err == nil {
		state.Status = multiagent.StatusRunning
		state.PID = cmd.Process.Pid
		state.StartedAt = now
		state.UpdatedAt = now
		state.FinishedAt = time.Time{}
		state.Error = ""
		state.ResultPath = ""
		_ = t.Coordinator.UpdateAgentState(runID, state)
	}

	_, _ = t.Coordinator.AppendEvent(runID, agentID, multiagent.AgentEvent{
		Type:      "revived",
		Message:   "worker process revived after terminal state",
		CreatedAt: now,
		Payload: map[string]any{
			"pid":        cmd.Process.Pid,
			"executable": executable,
			"args":       strings.Join(cmdArgs, " "),
			"work_dir":   workDir,
		},
	})

	return cmd.Process.Pid, nil
}

type AgentEventsTool struct {
	Coordinator *multiagent.Coordinator
}

type agentEventsArgs struct {
	RunID    string `json:"run_id"`
	AgentID  string `json:"agent_id"`
	AfterSeq int64  `json:"after_seq"`
	Limit    int    `json:"limit"`
}

func (t *AgentEventsTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "agent_events",
			Description: "Read child-agent events from events.jsonl.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":    map[string]any{"type": "string"},
					"agent_id":  map[string]any{"type": "string"},
					"after_seq": map[string]any{"type": "integer"},
					"limit":     map[string]any{"type": "integer"},
				},
				"required": []string{"run_id", "agent_id"},
			},
		},
	}
}

func (t *AgentEventsTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Coordinator == nil {
		return "", errors.New("multi-agent coordinator is not configured")
	}
	var in agentEventsArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.RunID) == "" || strings.TrimSpace(in.AgentID) == "" {
		return "", errors.New("run_id and agent_id are required")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	events, err := t.Coordinator.ReadEvents(in.RunID, in.AgentID, in.AfterSeq, limit)
	if err != nil {
		return "", err
	}
	return prettyJSON(map[string]any{
		"run_id":    in.RunID,
		"agent_id":  in.AgentID,
		"after_seq": in.AfterSeq,
		"count":     len(events),
		"events":    events,
	})
}

type AgentResultTool struct {
	Coordinator *multiagent.Coordinator
}

type agentResultArgs struct {
	RunID   string `json:"run_id"`
	AgentID string `json:"agent_id"`
}

func (t *AgentResultTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "agent_result",
			Description: "Read final result of a child agent.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":   map[string]any{"type": "string"},
					"agent_id": map[string]any{"type": "string"},
				},
				"required": []string{"run_id", "agent_id"},
			},
		},
	}
}

func (t *AgentResultTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Coordinator == nil {
		return "", errors.New("multi-agent coordinator is not configured")
	}
	var in agentResultArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.RunID) == "" || strings.TrimSpace(in.AgentID) == "" {
		return "", errors.New("run_id and agent_id are required")
	}
	result, err := t.Coordinator.ReadResult(in.RunID, in.AgentID)
	if err != nil {
		return "", err
	}
	return prettyJSON(result)
}

type AgentSignalSendTool struct {
	Coordinator *multiagent.Coordinator
}

type agentSignalSendArgs struct {
	RunID       string         `json:"run_id"`
	Key         string         `json:"key"`
	FromAgentID string         `json:"from_agent_id"`
	Payload     map[string]any `json:"payload"`
}

func (t *AgentSignalSendTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name: "agent_signal_send",
			Description: "Publish a signal event to run-level JSON channel. " +
				"Use together with agent_signal_wait for cross-agent synchronization.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":        map[string]any{"type": "string"},
					"key":           map[string]any{"type": "string"},
					"from_agent_id": map[string]any{"type": "string"},
					"payload":       map[string]any{"type": "object"},
				},
				"required": []string{"run_id", "key"},
			},
		},
	}
}

func (t *AgentSignalSendTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Coordinator == nil {
		return "", errors.New("multi-agent coordinator is not configured")
	}
	var in agentSignalSendArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.RunID) == "" || strings.TrimSpace(in.Key) == "" {
		return "", errors.New("run_id and key are required")
	}
	sig, err := t.Coordinator.AppendSignal(in.RunID, in.Key, multiagent.Signal{
		Key:         in.Key,
		FromAgentID: strings.TrimSpace(in.FromAgentID),
		Payload:     in.Payload,
		CreatedAt:   time.Now().UTC(),
	})
	if err != nil {
		return "", err
	}
	return prettyJSON(sig)
}

type AgentSignalWaitTool struct {
	Coordinator *multiagent.Coordinator
}

type agentSignalWaitArgs struct {
	RunID          string `json:"run_id"`
	Key            string `json:"key"`
	AfterSeq       int64  `json:"after_seq"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	PollMs         int    `json:"poll_ms"`
}

func (t *AgentSignalWaitTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "agent_signal_wait",
			Description: "Block until a new signal appears for the given key (JSON-file based synchronization).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":          map[string]any{"type": "string"},
					"key":             map[string]any{"type": "string"},
					"after_seq":       map[string]any{"type": "integer"},
					"timeout_seconds": map[string]any{"type": "integer"},
					"poll_ms":         map[string]any{"type": "integer"},
				},
				"required": []string{"run_id", "key"},
			},
		},
	}
}

func (t *AgentSignalWaitTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Coordinator == nil {
		return "", errors.New("multi-agent coordinator is not configured")
	}
	var in agentSignalWaitArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.RunID) == "" || strings.TrimSpace(in.Key) == "" {
		return "", errors.New("run_id and key are required")
	}
	poll := 300 * time.Millisecond
	if in.PollMs > 0 {
		poll = time.Duration(in.PollMs) * time.Millisecond
	}
	timeout := time.Duration(in.TimeoutSeconds) * time.Second
	sig, err := t.Coordinator.WaitForSignal(ctx, in.RunID, in.Key, in.AfterSeq, timeout, poll)
	if err != nil {
		if errors.Is(err, multiagent.ErrSignalWaitTimeout) {
			return prettyJSON(map[string]any{
				"run_id":    in.RunID,
				"key":       in.Key,
				"after_seq": in.AfterSeq,
				"timed_out": true,
			})
		}
		return "", err
	}
	return prettyJSON(map[string]any{
		"timed_out": false,
		"signal":    sig,
	})
}

func prettyJSON(value any) (string, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
