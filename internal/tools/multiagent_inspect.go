package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/multiagent"
)

type AgentRunListTool struct {
	Coordinator *multiagent.Coordinator
}

type agentRunListArgs struct {
	ActiveOnly    *bool `json:"active_only"`
	IncludeAgents *bool `json:"include_agents"`
	IncludeTasks  *bool `json:"include_tasks"`
	Limit         int   `json:"limit"`
	Offset        int   `json:"offset"`
}

func (t *AgentRunListTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name: "agent_run_list",
			Description: "List multi-agent runs under the coordinator root. " +
				"Useful for answering 'what is currently running' when you don't know run_id.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"active_only":    map[string]any{"type": "boolean"},
					"include_agents": map[string]any{"type": "boolean"},
					"include_tasks":  map[string]any{"type": "boolean"},
					"limit":          map[string]any{"type": "integer"},
					"offset":         map[string]any{"type": "integer"},
				},
			},
		},
	}
}

func (t *AgentRunListTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Coordinator == nil {
		return "", errors.New("multi-agent coordinator is not configured")
	}
	var in agentRunListArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
	}

	activeOnly := true
	if in.ActiveOnly != nil {
		activeOnly = *in.ActiveOnly
	}
	includeAgents := true
	if in.IncludeAgents != nil {
		includeAgents = *in.IncludeAgents
	}
	includeTasks := true
	if in.IncludeTasks != nil {
		includeTasks = *in.IncludeTasks
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	offset := in.Offset
	if offset < 0 {
		offset = 0
	}

	runs, err := t.Coordinator.ListRuns()
	if err != nil {
		return "", err
	}

	type agentItem struct {
		SpecID     string    `json:"agent_id"`
		Status     string    `json:"status"`
		PID        int       `json:"pid,omitempty"`
		StartedAt  time.Time `json:"started_at,omitempty"`
		UpdatedAt  time.Time `json:"updated_at"`
		FinishedAt time.Time `json:"finished_at,omitempty"`
		Error      string    `json:"error,omitempty"`
		Task       string    `json:"task,omitempty"`
		AgentDir   string    `json:"agent_dir,omitempty"`
	}
	type runItem struct {
		RunID     string         `json:"run_id"`
		CreatedAt time.Time      `json:"created_at"`
		Metadata  map[string]any `json:"metadata,omitempty"`
		RunDir    string         `json:"run_dir"`
		Summary   map[string]int `json:"summary"`
		Agents    []agentItem    `json:"agents,omitempty"`
	}

	filtered := make([]runItem, 0, len(runs))
	for _, run := range runs {
		states, err := t.Coordinator.ListAgentStates(run.ID)
		if err != nil {
			continue
		}

		summary := map[string]int{
			"total":     len(states),
			"active":    0,
			"terminal":  0,
			"pending":   0,
			"running":   0,
			"paused":    0,
			"completed": 0,
			"failed":    0,
			"canceled":  0,
		}
		for _, st := range states {
			switch strings.ToLower(strings.TrimSpace(st.Status)) {
			case multiagent.StatusPending:
				summary["pending"]++
				summary["active"]++
			case multiagent.StatusRunning:
				summary["running"]++
				summary["active"]++
			case multiagent.StatusPaused:
				summary["paused"]++
				summary["active"]++
			case multiagent.StatusCompleted:
				summary["completed"]++
				summary["terminal"]++
			case multiagent.StatusFailed:
				summary["failed"]++
				summary["terminal"]++
			case multiagent.StatusCanceled:
				summary["canceled"]++
				summary["terminal"]++
			default:
				// unknown status: do not categorize
			}
		}
		if activeOnly && summary["active"] == 0 {
			continue
		}

		item := runItem{
			RunID:     run.ID,
			CreatedAt: run.CreatedAt,
			Metadata:  run.Metadata,
			RunDir:    t.Coordinator.RunDir(run.ID),
			Summary:   summary,
		}

		if includeAgents {
			agents := make([]agentItem, 0, len(states))
			for _, st := range states {
				ai := agentItem{
					SpecID:     st.AgentID,
					Status:     st.Status,
					PID:        st.PID,
					StartedAt:  st.StartedAt,
					UpdatedAt:  st.UpdatedAt,
					FinishedAt: st.FinishedAt,
					Error:      st.Error,
					AgentDir:   t.Coordinator.AgentDir(run.ID, st.AgentID),
				}
				if includeTasks {
					spec, err := t.Coordinator.ReadAgentSpec(run.ID, st.AgentID)
					if err == nil {
						ai.Task = strings.TrimSpace(spec.Task)
					}
				}
				if len(ai.Task) > 300 {
					ai.Task = ai.Task[:300] + "…"
				}
				agents = append(agents, ai)
			}
			sort.Slice(agents, func(i, j int) bool { return agents[i].SpecID < agents[j].SpecID })
			item.Agents = agents
		}

		filtered = append(filtered, item)
	}

	if offset >= len(filtered) {
		return prettyJSON(map[string]any{
			"coordinator_root": t.Coordinator.RunRoot,
			"active_only":      activeOnly,
			"offset":           offset,
			"limit":            limit,
			"count":            0,
			"runs":             []runItem{},
		})
	}

	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	page := filtered[offset:end]

	return prettyJSON(map[string]any{
		"coordinator_root": t.Coordinator.RunRoot,
		"active_only":      activeOnly,
		"offset":           offset,
		"limit":            limit,
		"count":            len(page),
		"total":            len(filtered),
		"runs":             page,
	})
}

type AgentInspectTool struct {
	Coordinator *multiagent.Coordinator
}

type agentInspectArgs struct {
	RunID           string `json:"run_id"`
	AgentID         string `json:"agent_id"`
	EventsLimit     int    `json:"events_limit"`
	CommandsLimit   int    `json:"commands_limit"`
	StdoutTailLines int    `json:"stdout_tail_lines"`
	StderrTailLines int    `json:"stderr_tail_lines"`
	MaxBytes        int    `json:"max_bytes"`
}

func (t *AgentInspectTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name: "agent_inspect",
			Description: "Load a child agent execution context for troubleshooting: spec/state, recent events & commands, and stdout/stderr tail.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":             map[string]any{"type": "string"},
					"agent_id":           map[string]any{"type": "string"},
					"events_limit":       map[string]any{"type": "integer"},
					"commands_limit":     map[string]any{"type": "integer"},
					"stdout_tail_lines":  map[string]any{"type": "integer"},
					"stderr_tail_lines":  map[string]any{"type": "integer"},
					"max_bytes":          map[string]any{"type": "integer"},
				},
				"required": []string{"run_id", "agent_id"},
			},
		},
	}
}

func (t *AgentInspectTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Coordinator == nil {
		return "", errors.New("multi-agent coordinator is not configured")
	}
	var in agentInspectArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	runID := strings.TrimSpace(in.RunID)
	agentID := strings.TrimSpace(in.AgentID)
	if runID == "" || agentID == "" {
		return "", errors.New("run_id and agent_id are required")
	}

	eventsLimit := in.EventsLimit
	if eventsLimit <= 0 {
		eventsLimit = 80
	}
	commandsLimit := in.CommandsLimit
	if commandsLimit <= 0 {
		commandsLimit = 40
	}
	stdoutLines := in.StdoutTailLines
	if stdoutLines <= 0 {
		stdoutLines = 80
	}
	stderrLines := in.StderrTailLines
	if stderrLines <= 0 {
		stderrLines = 80
	}
	maxBytes := in.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 128 * 1024
	}

	spec, specErr := t.Coordinator.ReadAgentSpec(runID, agentID)
	state, stateErr := t.Coordinator.ReadAgentState(runID, agentID)
	result, resultErr := t.Coordinator.ReadResult(runID, agentID)

	agentDir := t.Coordinator.AgentDir(runID, agentID)
	commandsPath := t.Coordinator.AgentCommandsPath(runID, agentID)
	eventsPath := t.Coordinator.AgentEventsPath(runID, agentID)
	resultPath := t.Coordinator.AgentResultPath(runID, agentID)
	stdoutPath := filepath.Join(agentDir, "stdout.log")
	stderrPath := filepath.Join(agentDir, "stderr.log")

	events, _ := multiagent.TailJSONL[multiagent.AgentEvent](eventsPath, eventsLimit, maxBytes)
	commands, _ := multiagent.TailJSONL[multiagent.AgentCommand](commandsPath, commandsLimit, maxBytes)
	stdoutTail, _ := multiagent.TailFileText(stdoutPath, stdoutLines, maxBytes)
	stderrTail, _ := multiagent.TailFileText(stderrPath, stderrLines, maxBytes)

	errs := make([]string, 0, 3)
	if specErr != nil {
		errs = append(errs, "spec: "+specErr.Error())
	}
	if stateErr != nil {
		errs = append(errs, "state: "+stateErr.Error())
	}
	if resultErr != nil && !errors.Is(resultErr, os.ErrNotExist) {
		errs = append(errs, "result: "+resultErr.Error())
	}

	out := map[string]any{
		"run_id":   runID,
		"agent_id": agentID,
		"paths": map[string]any{
			"agent_dir":     agentDir,
			"spec_path":     t.Coordinator.AgentSpecPath(runID, agentID),
			"state_path":    t.Coordinator.AgentStatePath(runID, agentID),
			"commands_path": commandsPath,
			"events_path":   eventsPath,
			"result_path":   resultPath,
			"stdout_log":    stdoutPath,
			"stderr_log":    stderrPath,
		},
		"events":        events,
		"commands":      commands,
		"stdout_tail":   stdoutTail,
		"stderr_tail":   stderrTail,
		"errors":        errs,
		"inspected_at":  time.Now().UTC(),
	}
	if specErr == nil {
		out["spec"] = spec
	}
	if stateErr == nil {
		out["state"] = state
	}
	if resultErr == nil {
		out["result"] = result
	} else if errors.Is(resultErr, os.ErrNotExist) {
		out["result"] = nil
	}

	return prettyJSON(out)
}
