package agent

import (
	"context"
	"errors"
	"strings"

	"test_skill_agent/internal/llm"
)

type HeadlessSessionHooks struct {
	Emit      func(llm.Message)
	AfterTool func(call llm.ToolCall, result string, callErr error) []llm.Message
}

// RunHeadlessSession runs a single multi-step "turn" (like the TUI) without any UI.
// It injects the given run_id into agent_* tool calls automatically and returns the
// final assistant content for the turn.
func (a *Agent) RunHeadlessSession(ctx context.Context, runID string, userText string, baseHistory []llm.Message) (string, error) {
	return a.RunHeadlessSessionWithHooks(ctx, runID, userText, baseHistory, HeadlessSessionHooks{})
}

// RunHeadlessSessionWithHooks runs a headless turn and optionally emits per-message callbacks.
// Hooks are best-effort and must be side-effect free.
func (a *Agent) RunHeadlessSessionWithHooks(ctx context.Context, runID string, userText string, baseHistory []llm.Message, hooks HeadlessSessionHooks) (string, error) {
	if a == nil {
		return "", errors.New("agent is nil")
	}
	final := ""
	emit := func(msg llm.Message) {
		if msg.Role == "assistant" && len(msg.ToolCalls) == 0 {
			final = msg.Content
		}
		if hooks.Emit != nil {
			hooks.Emit(msg)
		}
	}
	if err := runTUITurnStreaming(ctx, a, runID, "", userText, baseHistory, emit, hooks.AfterTool); err != nil {
		return "", err
	}
	return strings.TrimSpace(final), nil
}
