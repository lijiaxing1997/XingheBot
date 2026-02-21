package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/multiagent"
)

type SubagentsTool struct {
	Coordinator *multiagent.Coordinator
	steerMu     sync.Mutex
	steerLast   map[string]time.Time
}

type subagentsArgs struct {
	Action           string `json:"action"`
	RunID            string `json:"run_id"`
	Target           string `json:"target"`
	Message          string `json:"message"`
	Role             string `json:"role"`
	RecentMinutes    int    `json:"recent_minutes"`
	EventsLimit      int    `json:"events_limit"`
	TaskPreviewChars *int   `json:"task_preview_chars"`
	Force            bool   `json:"force"`
}

func (t *SubagentsTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name: "subagents",
			Description: "Unified child-agent orchestration. " +
				"Actions: list (progress snapshot), steer (send guidance message), kill (cancel or force-kill). " +
				"Targets: agent_id, \"last\", numeric index from list output, or \"run_id/agent_id\".",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type": "string",
						"enum": []string{"list", "steer", "kill"},
					},
					"run_id":  map[string]any{"type": "string"},
					"target":  map[string]any{"type": "string"},
					"message": map[string]any{"type": "string"},
					"role": map[string]any{
						"type": "string",
						"enum": []string{"user", "system"},
					},
					"recent_minutes":     map[string]any{"type": "integer"},
					"events_limit":       map[string]any{"type": "integer"},
					"task_preview_chars": map[string]any{"type": "integer"},
					"force":              map[string]any{"type": "boolean"},
				},
			},
		},
	}
}

const (
	defaultRecentMinutes    = 30
	maxRecentMinutes        = 24 * 60
	defaultEventsLimit      = 6
	defaultTaskPreviewChars = 200
	maxSteerMessageChars    = 4000
	steerRateLimit          = 2 * time.Second
)

var digitsOnly = regexp.MustCompile(`^\d+$`)

func (t *SubagentsTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Coordinator == nil {
		return "", errors.New("multi-agent coordinator is not configured")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var in subagentsArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
	}

	action := strings.ToLower(strings.TrimSpace(in.Action))
	if action == "" {
		action = "list"
	}

	runID := strings.TrimSpace(in.RunID)
	target := strings.TrimSpace(in.Target)
	if runID == "" && (action == "steer" || action == "kill") && strings.Contains(target, "/") {
		parts := strings.SplitN(target, "/", 2)
		if strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != "" {
			runID = strings.TrimSpace(parts[0])
			target = strings.TrimSpace(parts[1])
		}
	}

	recentMinutes := in.RecentMinutes
	if recentMinutes <= 0 {
		recentMinutes = defaultRecentMinutes
	}
	if recentMinutes > maxRecentMinutes {
		recentMinutes = maxRecentMinutes
	}
	eventsLimit := in.EventsLimit
	if eventsLimit <= 0 {
		eventsLimit = defaultEventsLimit
	}
	taskPreviewChars := defaultTaskPreviewChars
	if in.TaskPreviewChars != nil {
		taskPreviewChars = *in.TaskPreviewChars
		if taskPreviewChars < 0 {
			taskPreviewChars = 0
		}
	}

	switch action {
	case "list":
		return t.list(runID, recentMinutes, eventsLimit, taskPreviewChars)
	case "steer":
		return t.steer(runID, target, in.Message, in.Role, recentMinutes, eventsLimit, taskPreviewChars)
	case "kill":
		return t.kill(runID, target, in.Force, recentMinutes, eventsLimit, taskPreviewChars)
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

type subagentEventSummary struct {
	Seq       int64     `json:"seq"`
	Type      string    `json:"type"`
	Message   string    `json:"message,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type subagentView struct {
	Index       int                   `json:"index"`
	RunID       string                `json:"run_id,omitempty"`
	AgentID     string                `json:"agent_id"`
	Status      string                `json:"status"`
	PID         int                   `json:"pid,omitempty"`
	Runtime     string                `json:"runtime,omitempty"`
	RuntimeMs   int64                 `json:"runtime_ms,omitempty"`
	StartedAt   time.Time             `json:"started_at,omitempty"`
	UpdatedAt   time.Time             `json:"updated_at"`
	FinishedAt  time.Time             `json:"finished_at,omitempty"`
	Error       string                `json:"error,omitempty"`
	TaskPreview string                `json:"task_preview,omitempty"`
	LastEvent   *subagentEventSummary `json:"last_event,omitempty"`

	// For internal sorting/selection only.
	sortTime time.Time
	active   bool
}

func (t *SubagentsTool) list(runID string, recentMinutes int, eventsLimit int, taskPreviewChars int) (string, error) {
	now := time.Now().UTC()
	cutoff := now.Add(-time.Duration(recentMinutes) * time.Minute)

	active, recent, err := t.collectViews(runID, cutoff, now, eventsLimit, taskPreviewChars)
	if err != nil {
		return "", err
	}
	assignIndices(active, recent)

	out := map[string]any{
		"status":         "ok",
		"action":         "list",
		"run_id":         strings.TrimSpace(runID),
		"recent_minutes": recentMinutes,
		"count":          len(active) + len(recent),
		"active":         stripInternalFields(active),
		"recent":         stripInternalFields(recent),
		"text":           buildListText(active, recent, recentMinutes),
		"checked_at":     now,
	}
	return prettyJSON(out)
}

func (t *SubagentsTool) steer(runID string, target string, message string, role string, recentMinutes int, eventsLimit int, taskPreviewChars int) (string, error) {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return "", errors.New("message is required for steer")
	}
	if len(msg) > maxSteerMessageChars {
		return "", fmt.Errorf("message too long: %d (max=%d)", len(msg), maxSteerMessageChars)
	}
	normalizedRole := strings.ToLower(strings.TrimSpace(role))
	if normalizedRole != "system" {
		normalizedRole = "user"
	}

	agentRunID := strings.TrimSpace(runID)
	targetToken := strings.TrimSpace(target)
	if agentRunID == "" {
		resolved, err := t.resolveAcrossRuns(targetToken, recentMinutes, eventsLimit, taskPreviewChars)
		if err != nil {
			return "", err
		}
		agentRunID = resolved.RunID
		targetToken = resolved.AgentID
	}

	if agentRunID == "" {
		return "", errors.New("run_id is required for steer (or provide target as run_id/agent_id)")
	}
	agent, err := t.resolveWithinRun(agentRunID, targetToken, recentMinutes, eventsLimit, taskPreviewChars)
	if err != nil {
		return "", err
	}

	key := agentRunID + "/" + agent.AgentID
	t.steerMu.Lock()
	if t.steerLast == nil {
		t.steerLast = make(map[string]time.Time)
	}
	last := t.steerLast[key]
	if !last.IsZero() && time.Since(last) < steerRateLimit {
		t.steerMu.Unlock()
		return "", fmt.Errorf("rate limited: steer too fast for %s (min=%s)", key, steerRateLimit.String())
	}
	t.steerLast[key] = time.Now().UTC()
	t.steerMu.Unlock()

	cmd, err := t.Coordinator.AppendCommand(agentRunID, agent.AgentID, multiagent.AgentCommand{
		Type: multiagent.CommandMessage,
		Payload: map[string]any{
			"text": msg,
			"role": normalizedRole,
		},
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return "", err
	}
	_, _ = t.Coordinator.AppendEvent(agentRunID, agent.AgentID, multiagent.AgentEvent{
		Type:      "steer_requested",
		Message:   "steer message queued",
		CreatedAt: time.Now().UTC(),
		Payload: map[string]any{
			"role":          normalizedRole,
			"message_chars": len(msg),
		},
	})

	return prettyJSON(map[string]any{
		"status":   "ok",
		"action":   "steer",
		"run_id":   agentRunID,
		"agent_id": agent.AgentID,
		"role":     normalizedRole,
		"command":  cmd,
		"note":     "message will be injected before the child agent's next model call",
	})
}

func (t *SubagentsTool) kill(runID string, target string, force bool, recentMinutes int, eventsLimit int, taskPreviewChars int) (string, error) {
	agentRunID := strings.TrimSpace(runID)
	targetToken := strings.TrimSpace(target)
	if agentRunID == "" && strings.Contains(targetToken, "/") {
		parts := strings.SplitN(targetToken, "/", 2)
		if strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != "" {
			agentRunID = strings.TrimSpace(parts[0])
			targetToken = strings.TrimSpace(parts[1])
		}
	}
	if agentRunID == "" {
		return "", errors.New("run_id is required for kill (or provide target as run_id/agent_id)")
	}
	if targetToken == "" {
		return "", errors.New("target is required for kill (agent_id, last, number, or all/*)")
	}

	now := time.Now().UTC()
	states, err := t.Coordinator.ListAgentStates(agentRunID)
	if err != nil {
		return "", err
	}
	views, _ := t.viewsFromStates(agentRunID, states, now, eventsLimit, taskPreviewChars)
	ordered := orderViews(views, now.Add(-time.Duration(recentMinutes)*time.Minute))
	if len(ordered) == 0 {
		return "", errors.New("no agents found for run")
	}
	assignIndicesFromOrdered(ordered)

	targets := make([]subagentView, 0, 4)
	lower := strings.ToLower(targetToken)
	if lower == "all" || targetToken == "*" {
		for _, v := range ordered {
			if v.active {
				targets = append(targets, v)
			}
		}
		if len(targets) == 0 {
			return prettyJSON(map[string]any{
				"status": "ok",
				"action": "kill",
				"run_id": agentRunID,
				"target": targetToken,
				"killed": 0,
				"text":   "no running subagents to kill.",
			})
		}
	} else {
		resolved, err := resolveTargetFromOrdered(ordered, targetToken)
		if err != nil {
			return "", err
		}
		targets = append(targets, resolved)
	}

	type killResult struct {
		AgentID      string `json:"agent_id"`
		StatusBefore string `json:"status_before"`
		PID          int    `json:"pid,omitempty"`
		CancelQueued bool   `json:"cancel_queued"`
		ForceKilled  bool   `json:"force_killed"`
		Error        string `json:"error,omitempty"`
	}
	results := make([]killResult, 0, len(targets))
	killed := 0

	for _, v := range targets {
		kr := killResult{
			AgentID:      v.AgentID,
			StatusBefore: v.Status,
			PID:          v.PID,
			CancelQueued: false,
			ForceKilled:  false,
		}

		if multiagent.IsTerminalStatus(v.Status) {
			kr.Error = "agent already in terminal state"
			results = append(results, kr)
			continue
		}

		_, cmdErr := t.Coordinator.AppendCommand(agentRunID, v.AgentID, multiagent.AgentCommand{
			Type: multiagent.CommandCancel,
			Payload: map[string]any{
				"reason": "killed by operator via subagents tool",
				"force":  force,
			},
			CreatedAt: time.Now().UTC(),
		})
		if cmdErr == nil {
			kr.CancelQueued = true
			_, _ = t.Coordinator.AppendEvent(agentRunID, v.AgentID, multiagent.AgentEvent{
				Type:      "cancel_requested",
				Message:   "cancel queued by operator",
				CreatedAt: time.Now().UTC(),
				Payload: map[string]any{
					"force": force,
				},
			})
		} else {
			kr.Error = cmdErr.Error()
			results = append(results, kr)
			continue
		}

		if force {
			if v.PID <= 0 {
				kr.Error = "force requested but pid is missing"
				results = append(results, kr)
				continue
			}
			proc, err := os.FindProcess(v.PID)
			if err != nil {
				kr.Error = "find process: " + err.Error()
				results = append(results, kr)
				continue
			}
			if err := proc.Kill(); err != nil {
				kr.Error = "kill process: " + err.Error()
				results = append(results, kr)
				continue
			}
			kr.ForceKilled = true
			killed++

			now := time.Now().UTC()
			state, err := t.Coordinator.ReadAgentState(agentRunID, v.AgentID)
			if err == nil && !multiagent.IsTerminalStatus(state.Status) {
				state.Status = multiagent.StatusCanceled
				state.Error = "killed by operator"
				state.FinishedAt = now
				state.UpdatedAt = now
				_ = t.Coordinator.UpdateAgentState(agentRunID, state)
				_ = t.Coordinator.WriteResult(agentRunID, v.AgentID, multiagent.AgentResult{
					RunID:      agentRunID,
					AgentID:    v.AgentID,
					Status:     multiagent.StatusCanceled,
					Error:      "killed by operator",
					FinishedAt: now,
				})
				_, _ = t.Coordinator.AppendEvent(agentRunID, v.AgentID, multiagent.AgentEvent{
					Type:      "process_killed",
					Message:   "process killed by operator",
					CreatedAt: now,
					Payload: map[string]any{
						"pid": v.PID,
					},
				})
			}
		}

		results = append(results, kr)
	}

	text := fmt.Sprintf("killed %d subagent%s.", killed, plural(killed))
	if !force {
		text = "cancel queued. use force=true to hard-kill the OS process."
	}

	return prettyJSON(map[string]any{
		"status":  "ok",
		"action":  "kill",
		"run_id":  agentRunID,
		"target":  target,
		"force":   force,
		"killed":  killed,
		"count":   len(results),
		"results": results,
		"text":    text,
	})
}

func (t *SubagentsTool) resolveWithinRun(runID string, target string, recentMinutes int, eventsLimit int, taskPreviewChars int) (subagentView, error) {
	if strings.TrimSpace(target) == "" {
		return subagentView{}, errors.New("target is required")
	}
	now := time.Now().UTC()
	cutoff := now.Add(-time.Duration(recentMinutes) * time.Minute)

	active, recent, err := t.collectViews(runID, cutoff, now, eventsLimit, taskPreviewChars)
	if err != nil {
		return subagentView{}, err
	}
	ordered := append([]subagentView{}, active...)
	ordered = append(ordered, recent...)
	assignIndicesFromOrdered(ordered)

	return resolveTargetFromOrdered(ordered, target)
}

func (t *SubagentsTool) resolveAcrossRuns(target string, recentMinutes int, eventsLimit int, taskPreviewChars int) (subagentView, error) {
	if strings.TrimSpace(target) == "" {
		return subagentView{}, errors.New("target is required")
	}
	now := time.Now().UTC()
	cutoff := now.Add(-time.Duration(recentMinutes) * time.Minute)
	active, recent, err := t.collectViews("", cutoff, now, eventsLimit, taskPreviewChars)
	if err != nil {
		return subagentView{}, err
	}
	assignIndices(active, recent)
	ordered := append([]subagentView{}, active...)
	ordered = append(ordered, recent...)
	return resolveTargetFromOrdered(ordered, target)
}

func (t *SubagentsTool) collectViews(runID string, cutoff time.Time, now time.Time, eventsLimit int, taskPreviewChars int) ([]subagentView, []subagentView, error) {
	views := make([]subagentView, 0, 16)

	if strings.TrimSpace(runID) == "" {
		runs, err := t.Coordinator.ListRuns()
		if err != nil {
			return nil, nil, err
		}
		for _, run := range runs {
			states, err := t.Coordinator.ListAgentStates(run.ID)
			if err != nil || len(states) == 0 {
				continue
			}
			v, _ := t.viewsFromStates(run.ID, states, now, eventsLimit, taskPreviewChars)
			views = append(views, v...)
		}
	} else {
		states, err := t.Coordinator.ListAgentStates(runID)
		if err != nil {
			return nil, nil, err
		}
		v, _ := t.viewsFromStates(runID, states, now, eventsLimit, taskPreviewChars)
		views = append(views, v...)
	}

	ordered := orderViews(views, cutoff)
	active := make([]subagentView, 0, len(ordered))
	recent := make([]subagentView, 0, len(ordered))
	for _, v := range ordered {
		if v.active {
			active = append(active, v)
		} else {
			recent = append(recent, v)
		}
	}
	return active, recent, nil
}

func (t *SubagentsTool) viewsFromStates(runID string, states []multiagent.AgentState, now time.Time, eventsLimit int, taskPreviewChars int) ([]subagentView, error) {
	if len(states) == 0 {
		return nil, nil
	}

	maxBytes := 64 * 1024
	views := make([]subagentView, 0, len(states))
	for _, st := range states {
		agentID := strings.TrimSpace(st.AgentID)
		if agentID == "" {
			continue
		}

		taskPreview := ""
		if taskPreviewChars > 0 {
			spec, err := t.Coordinator.ReadAgentSpec(runID, agentID)
			if err == nil {
				taskPreview = strings.TrimSpace(spec.Task)
				if len(taskPreview) > taskPreviewChars {
					taskPreview = taskPreview[:taskPreviewChars] + "…"
				}
			}
		}

		var lastEvent *subagentEventSummary
		eventsPath := t.Coordinator.AgentEventsPath(runID, agentID)
		events, _ := multiagent.TailJSONL[multiagent.AgentEvent](eventsPath, eventsLimit, maxBytes)
		if len(events) > 0 {
			evt := events[len(events)-1]
			lastEvent = &subagentEventSummary{
				Seq:       evt.Seq,
				Type:      strings.TrimSpace(evt.Type),
				Message:   strings.TrimSpace(evt.Message),
				CreatedAt: evt.CreatedAt,
			}
		}

		active := !multiagent.IsTerminalStatus(st.Status)
		sortTime := st.UpdatedAt
		if active {
			if !st.StartedAt.IsZero() {
				sortTime = st.StartedAt
			} else if !st.CreatedAt.IsZero() {
				sortTime = st.CreatedAt
			}
		} else if !st.FinishedAt.IsZero() {
			sortTime = st.FinishedAt
		}

		runtimeStart := st.StartedAt
		if runtimeStart.IsZero() {
			runtimeStart = st.CreatedAt
		}
		runtimeEnd := now
		if !active && !st.FinishedAt.IsZero() {
			runtimeEnd = st.FinishedAt
		}
		var runtime time.Duration
		if !runtimeStart.IsZero() && !runtimeEnd.IsZero() && runtimeEnd.After(runtimeStart) {
			runtime = runtimeEnd.Sub(runtimeStart)
		}

		view := subagentView{
			RunID:       runID,
			AgentID:     agentID,
			Status:      strings.TrimSpace(st.Status),
			PID:         st.PID,
			StartedAt:   st.StartedAt,
			UpdatedAt:   st.UpdatedAt,
			FinishedAt:  st.FinishedAt,
			Error:       strings.TrimSpace(st.Error),
			TaskPreview: taskPreview,
			LastEvent:   lastEvent,
			Runtime:     formatDurationCompact(runtime),
			RuntimeMs:   runtime.Milliseconds(),
			sortTime:    sortTime,
			active:      active,
		}
		views = append(views, view)
	}
	return views, nil
}

func orderViews(views []subagentView, cutoff time.Time) []subagentView {
	active := make([]subagentView, 0, len(views))
	recent := make([]subagentView, 0, len(views))
	for _, v := range views {
		if v.active {
			active = append(active, v)
			continue
		}
		when := v.FinishedAt
		if when.IsZero() {
			when = v.UpdatedAt
		}
		if when.IsZero() || when.Before(cutoff) {
			continue
		}
		recent = append(recent, v)
	}

	sort.Slice(active, func(i, j int) bool {
		if active[i].sortTime.Equal(active[j].sortTime) {
			if active[i].RunID == active[j].RunID {
				return active[i].AgentID < active[j].AgentID
			}
			return active[i].RunID < active[j].RunID
		}
		return active[i].sortTime.After(active[j].sortTime)
	})
	sort.Slice(recent, func(i, j int) bool {
		if recent[i].sortTime.Equal(recent[j].sortTime) {
			if recent[i].RunID == recent[j].RunID {
				return recent[i].AgentID < recent[j].AgentID
			}
			return recent[i].RunID < recent[j].RunID
		}
		return recent[i].sortTime.After(recent[j].sortTime)
	})

	out := make([]subagentView, 0, len(active)+len(recent))
	out = append(out, active...)
	out = append(out, recent...)
	return out
}

func assignIndices(active []subagentView, recent []subagentView) {
	index := 1
	for i := range active {
		active[i].Index = index
		index++
	}
	for i := range recent {
		recent[i].Index = index
		index++
	}
}

func assignIndicesFromOrdered(ordered []subagentView) {
	for i := range ordered {
		ordered[i].Index = i + 1
	}
}

func stripInternalFields(views []subagentView) []subagentView {
	out := make([]subagentView, 0, len(views))
	for _, v := range views {
		v.sortTime = time.Time{}
		v.active = false
		out = append(out, v)
	}
	return out
}

func buildListText(active []subagentView, recent []subagentView, recentMinutes int) string {
	lines := make([]string, 0, 4+len(active)+len(recent))
	lines = append(lines, "active subagents:")
	if len(active) == 0 {
		lines = append(lines, "(none)")
	} else {
		for _, v := range active {
			lines = append(lines, formatListLine(v))
		}
	}
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("recent (last %dm):", recentMinutes))
	if len(recent) == 0 {
		lines = append(lines, "(none)")
	} else {
		for _, v := range recent {
			lines = append(lines, formatListLine(v))
		}
	}
	return strings.Join(lines, "\n")
}

func formatListLine(v subagentView) string {
	label := v.AgentID
	if v.RunID != "" {
		label = v.RunID + "/" + v.AgentID
	}
	parts := []string{}
	if v.Runtime != "" {
		parts = append(parts, v.Runtime)
	}
	status := strings.TrimSpace(v.Status)
	if status == "" {
		status = "unknown"
	}
	parts = append(parts, status)
	if v.LastEvent != nil && strings.TrimSpace(v.LastEvent.Type) != "" {
		evt := v.LastEvent.Type
		if m := strings.TrimSpace(v.LastEvent.Message); m != "" {
			evt = evt + ":" + truncateLine(m, 48)
		}
		parts = append(parts, evt)
	}
	line := fmt.Sprintf("%d. %s (%s)", v.Index, label, strings.Join(parts, ", "))
	if tp := strings.TrimSpace(v.TaskPreview); tp != "" {
		line += " - " + truncateLine(tp, 72)
	}
	return line
}

func resolveTargetFromOrdered(ordered []subagentView, token string) (subagentView, error) {
	if len(ordered) == 0 {
		return subagentView{}, errors.New("no agents available")
	}
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return subagentView{}, errors.New("missing target")
	}
	if strings.EqualFold(trimmed, "last") {
		return ordered[0], nil
	}
	if digitsOnly.MatchString(trimmed) {
		idx, err := strconv.Atoi(trimmed)
		if err != nil || idx <= 0 || idx > len(ordered) {
			return subagentView{}, fmt.Errorf("invalid subagent index: %s", trimmed)
		}
		return ordered[idx-1], nil
	}
	normalized := strings.TrimSpace(trimmed)
	for _, v := range ordered {
		if v.AgentID == normalized {
			return v, nil
		}
		if v.RunID != "" && (v.RunID+"/"+v.AgentID) == normalized {
			return v, nil
		}
	}
	return subagentView{}, fmt.Errorf("unknown subagent target: %s", trimmed)
}

func formatDurationCompact(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	sec := int64(d.Seconds())
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	min := sec / 60
	if min < 60 {
		return fmt.Sprintf("%dm%ds", min, sec%60)
	}
	h := min / 60
	return fmt.Sprintf("%dh%dm", h, min%60)
}

func truncateLine(s string, max int) string {
	text := strings.TrimSpace(s)
	if max <= 0 || len(text) <= max {
		return text
	}
	return text[:max] + "…"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
