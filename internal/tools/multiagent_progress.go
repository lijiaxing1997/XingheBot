package tools

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/multiagent"
)

type AgentProgressTool struct {
	Coordinator *multiagent.Coordinator
}

type agentProgressArgs struct {
	RunID            string `json:"run_id"`
	AgentID          string `json:"agent_id"`
	EventsLimit      int    `json:"events_limit"`
	StdoutTailLines  int    `json:"stdout_tail_lines"`
	StderrTailLines  int    `json:"stderr_tail_lines"`
	MaxBytes         int    `json:"max_bytes"`
	TaskPreviewChars int    `json:"task_preview_chars"`
}

func (t *AgentProgressTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name: "agent_progress",
			Description: "Non-blocking progress snapshot for child agents. " +
				"Returns state + recent events, and (for a single agent) stdout/stderr tail. " +
				"Use this for quick progress checks instead of polling agent_events in a loop.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id":             map[string]any{"type": "string"},
					"agent_id":           map[string]any{"type": "string", "description": "optional; when omitted, returns progress for all agents in the run"},
					"events_limit":       map[string]any{"type": "integer"},
					"stdout_tail_lines":  map[string]any{"type": "integer"},
					"stderr_tail_lines":  map[string]any{"type": "integer"},
					"max_bytes":          map[string]any{"type": "integer"},
					"task_preview_chars": map[string]any{"type": "integer", "description": "max characters of task preview included (0 disables)"},
				},
				"required": []string{"run_id"},
			},
		},
	}
}

func (t *AgentProgressTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Coordinator == nil {
		return "", errors.New("multi-agent coordinator is not configured")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var in agentProgressArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
	}
	runID := strings.TrimSpace(in.RunID)
	if runID == "" {
		return "", errors.New("run_id is required")
	}

	eventsLimit := in.EventsLimit
	if eventsLimit <= 0 {
		eventsLimit = 12
	}
	stdoutLines := in.StdoutTailLines
	if stdoutLines <= 0 {
		stdoutLines = 30
	}
	stderrLines := in.StderrTailLines
	if stderrLines <= 0 {
		stderrLines = 30
	}
	maxBytes := in.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 96 * 1024
	}
	taskPreviewChars := in.TaskPreviewChars
	if taskPreviewChars < 0 {
		taskPreviewChars = 0
	}
	if taskPreviewChars == 0 {
		taskPreviewChars = 240
	}

	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return t.progressForRun(runID, eventsLimit, maxBytes, taskPreviewChars)
	}
	return t.progressForAgent(runID, agentID, eventsLimit, stdoutLines, stderrLines, maxBytes, taskPreviewChars)
}

func (t *AgentProgressTool) progressForRun(runID string, eventsLimit int, maxBytes int, taskPreviewChars int) (string, error) {
	states, err := t.Coordinator.ListAgentStates(runID)
	if err != nil {
		return "", err
	}

	type agentItem struct {
		AgentID      string               `json:"agent_id"`
		Status       string               `json:"status"`
		PID          int                  `json:"pid,omitempty"`
		StartedAt    time.Time            `json:"started_at,omitempty"`
		UpdatedAt    time.Time            `json:"updated_at"`
		FinishedAt   time.Time            `json:"finished_at,omitempty"`
		Error        string               `json:"error,omitempty"`
		TaskPreview  string               `json:"task_preview,omitempty"`
		LastEvent    *multiagent.AgentEvent `json:"last_event,omitempty"`
		RecentEvents []multiagent.AgentEvent `json:"recent_events,omitempty"`
	}

	items := make([]agentItem, 0, len(states))
	summary := map[string]int{
		"total":     len(states),
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
		case multiagent.StatusRunning:
			summary["running"]++
		case multiagent.StatusPaused:
			summary["paused"]++
		case multiagent.StatusCompleted:
			summary["completed"]++
		case multiagent.StatusFailed:
			summary["failed"]++
		case multiagent.StatusCanceled:
			summary["canceled"]++
		}

		var taskPreview string
		if taskPreviewChars > 0 {
			spec, err := t.Coordinator.ReadAgentSpec(runID, st.AgentID)
			if err == nil {
				taskPreview = strings.TrimSpace(spec.Task)
				if len(taskPreview) > taskPreviewChars {
					taskPreview = taskPreview[:taskPreviewChars] + "…"
				}
			}
		}

		eventsPath := t.Coordinator.AgentEventsPath(runID, st.AgentID)
		events, _ := multiagent.TailJSONL[multiagent.AgentEvent](eventsPath, eventsLimit, maxBytes)
		var lastEvent *multiagent.AgentEvent
		if len(events) > 0 {
			evt := events[len(events)-1]
			lastEvent = &evt
		}

		items = append(items, agentItem{
			AgentID:      st.AgentID,
			Status:       st.Status,
			PID:          st.PID,
			StartedAt:    st.StartedAt,
			UpdatedAt:    st.UpdatedAt,
			FinishedAt:   st.FinishedAt,
			Error:        st.Error,
			TaskPreview:  taskPreview,
			LastEvent:    lastEvent,
			RecentEvents: events,
		})
	}

	sort.Slice(items, func(i, j int) bool { return items[i].AgentID < items[j].AgentID })

	return prettyJSON(map[string]any{
		"run_id":     runID,
		"count":      len(items),
		"summary":    summary,
		"agents":     items,
		"checked_at": time.Now().UTC(),
	})
}

func (t *AgentProgressTool) progressForAgent(runID string, agentID string, eventsLimit int, stdoutLines int, stderrLines int, maxBytes int, taskPreviewChars int) (string, error) {
	state, stateErr := t.Coordinator.ReadAgentState(runID, agentID)
	spec, specErr := t.Coordinator.ReadAgentSpec(runID, agentID)

	agentDir := t.Coordinator.AgentDir(runID, agentID)
	eventsPath := t.Coordinator.AgentEventsPath(runID, agentID)
	stdoutPath := filepath.Join(agentDir, "stdout.log")
	stderrPath := filepath.Join(agentDir, "stderr.log")

	events, _ := multiagent.TailJSONL[multiagent.AgentEvent](eventsPath, eventsLimit, maxBytes)
	var lastEvent *multiagent.AgentEvent
	if len(events) > 0 {
		evt := events[len(events)-1]
		lastEvent = &evt
	}

	stdoutTail, _ := multiagent.TailFileText(stdoutPath, stdoutLines, maxBytes)
	stderrTail, _ := multiagent.TailFileText(stderrPath, stderrLines, maxBytes)

	errs := make([]string, 0, 2)
	if stateErr != nil {
		errs = append(errs, "state: "+stateErr.Error())
	}
	if specErr != nil {
		errs = append(errs, "spec: "+specErr.Error())
	}

	taskPreview := ""
	if specErr == nil && taskPreviewChars > 0 {
		taskPreview = strings.TrimSpace(spec.Task)
		if len(taskPreview) > taskPreviewChars {
			taskPreview = taskPreview[:taskPreviewChars] + "…"
		}
	}

	out := map[string]any{
		"run_id":     runID,
		"agent_id":   agentID,
		"paths": map[string]any{
			"agent_dir":    agentDir,
			"state_path":   t.Coordinator.AgentStatePath(runID, agentID),
			"spec_path":    t.Coordinator.AgentSpecPath(runID, agentID),
			"events_path":  eventsPath,
			"stdout_log":   stdoutPath,
			"stderr_log":   stderrPath,
		},
		"task_preview":  taskPreview,
		"recent_events": events,
		"last_event":    lastEvent,
		"stdout_tail":   stdoutTail,
		"stderr_tail":   stderrTail,
		"errors":        errs,
		"checked_at":    time.Now().UTC(),
	}
	if stateErr == nil {
		out["state"] = state
	}
	return prettyJSON(out)
}
