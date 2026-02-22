package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/multiagent"
)

const visibilityPrimaryAgentID = "primary"

type AgentSubagentHideTool struct {
	Coordinator *multiagent.Coordinator
}

type agentSubagentHideArgs struct {
	AgentID  string   `json:"agent_id"`
	AgentIDs []string `json:"agent_ids"`
	Reason   string   `json:"reason"`
}

func (t *AgentSubagentHideTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name: "agent_subagent_hide",
			Description: "Hide one or more subagents from the TUI Status panel and TAB cycling for the CURRENT session only. " +
				"This does NOT delete any files; it just marks agents as hidden (archived) in ui_state.json.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent_id":  map[string]any{"type": "string"},
					"agent_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"reason":    map[string]any{"type": "string"},
				},
			},
		},
	}
}

func (t *AgentSubagentHideTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Coordinator == nil {
		return "", errors.New("multi-agent coordinator is not configured")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	runID, ok := multiagent.SessionRunID(ctx)
	if !ok {
		return "", errors.New("agent_subagent_hide is session-bound and requires a session context (run_id)")
	}

	var in agentSubagentHideArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
	}

	ids, err := normalizeAgentIDList(in.AgentID, in.AgentIDs)
	if err != nil {
		return "", err
	}

	toHide, skipped, notFound := filterExistingAgents(t.Coordinator, runID, ids)
	if len(toHide) > 0 {
		if _, err := t.Coordinator.SetAgentsHidden(runID, toHide, true, in.Reason, time.Now().UTC()); err != nil {
			return "", err
		}
	}

	out := map[string]any{
		"status":     "ok",
		"run_id":     runID,
		"hidden":     toHide,
		"skipped":    skipped,
		"not_found":  notFound,
		"requested":  ids,
		"checked_at": time.Now().UTC(),
		"text":       fmt.Sprintf("hidden=%d skipped=%d not_found=%d", len(toHide), len(skipped), len(notFound)),
	}
	return prettyJSON(out)
}

type AgentSubagentShowTool struct {
	Coordinator *multiagent.Coordinator
}

type agentSubagentShowArgs struct {
	AgentID  string   `json:"agent_id"`
	AgentIDs []string `json:"agent_ids"`
}

func (t *AgentSubagentShowTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name: "agent_subagent_show",
			Description: "Re-show previously hidden (archived) subagents for the CURRENT session only. " +
				"This is the inverse of agent_subagent_hide.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent_id":  map[string]any{"type": "string"},
					"agent_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
			},
		},
	}
}

func (t *AgentSubagentShowTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Coordinator == nil {
		return "", errors.New("multi-agent coordinator is not configured")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	runID, ok := multiagent.SessionRunID(ctx)
	if !ok {
		return "", errors.New("agent_subagent_show is session-bound and requires a session context (run_id)")
	}

	var in agentSubagentShowArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
	}

	ids, err := normalizeAgentIDList(in.AgentID, in.AgentIDs)
	if err != nil {
		return "", err
	}

	// Showing an agent is UI-only; it is OK to "show" even if the agent is already
	// visible. We still validate existence to reduce confusion.
	toShow, skipped, notFound := filterExistingAgents(t.Coordinator, runID, ids)
	if len(toShow) > 0 {
		if _, err := t.Coordinator.SetAgentsHidden(runID, toShow, false, "", time.Now().UTC()); err != nil {
			return "", err
		}
	}

	out := map[string]any{
		"status":     "ok",
		"run_id":     runID,
		"shown":      toShow,
		"skipped":    skipped,
		"not_found":  notFound,
		"requested":  ids,
		"checked_at": time.Now().UTC(),
		"text":       fmt.Sprintf("shown=%d skipped=%d not_found=%d", len(toShow), len(skipped), len(notFound)),
	}
	return prettyJSON(out)
}

type AgentSubagentListTool struct {
	Coordinator *multiagent.Coordinator
}

type agentSubagentListArgs struct {
	Scope            string `json:"scope"` // hidden|visible|all
	Query            string `json:"query"`
	Limit            int    `json:"limit"`
	Offset           int    `json:"offset"`
	TaskPreviewChars int    `json:"task_preview_chars"`
}

func (t *AgentSubagentListTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name: "agent_subagent_list",
			Description: "List/search subagents for the CURRENT session only, with an option to include hidden ones. " +
				"Use this to discover which subagents were archived via agent_subagent_hide.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scope": map[string]any{
						"type": "string",
						"enum": []string{"hidden", "visible", "all"},
					},
					"query":              map[string]any{"type": "string"},
					"limit":              map[string]any{"type": "integer"},
					"offset":             map[string]any{"type": "integer"},
					"task_preview_chars": map[string]any{"type": "integer"},
				},
			},
		},
	}
}

func (t *AgentSubagentListTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Coordinator == nil {
		return "", errors.New("multi-agent coordinator is not configured")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	runID, ok := multiagent.SessionRunID(ctx)
	if !ok {
		return "", errors.New("agent_subagent_list is session-bound and requires a session context (run_id)")
	}

	var in agentSubagentListArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
	}

	scope := strings.ToLower(strings.TrimSpace(in.Scope))
	if scope == "" {
		scope = "hidden"
	}
	switch scope {
	case "hidden", "visible", "all":
	default:
		return "", fmt.Errorf("invalid scope: %s", scope)
	}

	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	offset := in.Offset
	if offset < 0 {
		offset = 0
	}
	taskPreviewChars := in.TaskPreviewChars
	if taskPreviewChars == 0 {
		taskPreviewChars = 200
	}
	if taskPreviewChars < 0 {
		taskPreviewChars = 0
	}
	query := strings.TrimSpace(in.Query)
	queryLower := strings.ToLower(query)

	ui, _ := t.Coordinator.ReadRunUIState(runID)
	hidden := ui.HiddenAgents

	states, err := t.Coordinator.ListAgentStates(runID)
	if err != nil {
		return "", err
	}

	type item struct {
		AgentID     string    `json:"agent_id"`
		Status      string    `json:"status"`
		Hidden      bool      `json:"hidden"`
		HiddenAt    time.Time `json:"hidden_at,omitempty"`
		Reason      string    `json:"reason,omitempty"`
		UpdatedAt   time.Time `json:"updated_at,omitempty"`
		FinishedAt  time.Time `json:"finished_at,omitempty"`
		Error       string    `json:"error,omitempty"`
		TaskPreview string    `json:"task_preview,omitempty"`
	}

	stateMap := make(map[string]multiagent.AgentState, len(states))
	items := make([]item, 0, len(states))
	for _, st := range states {
		if st.AgentID == visibilityPrimaryAgentID {
			continue
		}
		stateMap[st.AgentID] = st
		rec, isHidden := hidden[st.AgentID]
		it := item{
			AgentID:    st.AgentID,
			Status:     strings.TrimSpace(st.Status),
			Hidden:     isHidden,
			HiddenAt:   rec.HiddenAt,
			Reason:     strings.TrimSpace(rec.Reason),
			UpdatedAt:  st.UpdatedAt,
			FinishedAt: st.FinishedAt,
			Error:      strings.TrimSpace(st.Error),
		}
		if taskPreviewChars > 0 {
			if spec, err := t.Coordinator.ReadAgentSpec(runID, st.AgentID); err == nil {
				task := strings.TrimSpace(spec.Task)
				if taskPreviewChars > 0 && len(task) > taskPreviewChars {
					task = task[:taskPreviewChars] + "…"
				}
				it.TaskPreview = task
			}
		}
		items = append(items, it)
	}

	// Include "stale" hidden entries with missing agent dirs/state (rare, but useful).
	for agentID, rec := range hidden {
		id := strings.TrimSpace(agentID)
		if id == "" || id == visibilityPrimaryAgentID {
			continue
		}
		if _, ok := stateMap[id]; ok {
			continue
		}
		items = append(items, item{
			AgentID:  id,
			Status:   "missing",
			Hidden:   true,
			HiddenAt: rec.HiddenAt,
			Reason:   strings.TrimSpace(rec.Reason),
		})
	}

	filtered := make([]item, 0, len(items))
	for _, it := range items {
		switch scope {
		case "hidden":
			if !it.Hidden {
				continue
			}
		case "visible":
			if it.Hidden {
				continue
			}
		}
		if queryLower != "" {
			hay := strings.ToLower(strings.Join([]string{
				it.AgentID,
				it.Status,
				it.Reason,
				it.Error,
				it.TaskPreview,
			}, "\n"))
			if !strings.Contains(hay, queryLower) {
				continue
			}
		}
		filtered = append(filtered, it)
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].AgentID < filtered[j].AgentID
	})

	total := len(filtered)
	if offset >= total {
		filtered = nil
	} else {
		end := offset + limit
		if end > total {
			end = total
		}
		filtered = filtered[offset:end]
	}

	return prettyJSON(map[string]any{
		"status":     "ok",
		"run_id":     runID,
		"scope":      scope,
		"query":      query,
		"offset":     offset,
		"limit":      limit,
		"count":      len(filtered),
		"total":      total,
		"items":      filtered,
		"checked_at": time.Now().UTC(),
	})
}

func normalizeAgentIDList(single string, list []string) ([]string, error) {
	out := make([]string, 0, 1+len(list))
	if strings.TrimSpace(single) != "" {
		out = append(out, strings.TrimSpace(single))
	}
	for _, v := range list {
		if strings.TrimSpace(v) == "" {
			continue
		}
		out = append(out, strings.TrimSpace(v))
	}
	if len(out) == 0 {
		return nil, errors.New("agent_id (or agent_ids) is required")
	}
	seen := make(map[string]bool, len(out))
	deduped := make([]string, 0, len(out))
	for _, id := range out {
		if seen[id] {
			continue
		}
		seen[id] = true
		deduped = append(deduped, id)
	}
	sort.Strings(deduped)
	return deduped, nil
}

func filterExistingAgents(coord *multiagent.Coordinator, runID string, agentIDs []string) (existing []string, skipped []string, notFound []string) {
	existing = make([]string, 0, len(agentIDs))
	skipped = make([]string, 0, 1)
	notFound = make([]string, 0, 4)

	for _, id := range agentIDs {
		agentID := strings.TrimSpace(id)
		if agentID == "" {
			continue
		}
		if agentID == visibilityPrimaryAgentID {
			skipped = append(skipped, agentID)
			continue
		}
		if _, err := coord.ReadAgentState(runID, agentID); err != nil {
			notFound = append(notFound, agentID)
			continue
		}
		existing = append(existing, agentID)
	}

	sort.Strings(existing)
	sort.Strings(skipped)
	sort.Strings(notFound)
	return existing, skipped, notFound
}
