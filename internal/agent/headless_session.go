package agent

import (
	"context"
	"errors"
	"strings"

	"test_skill_agent/internal/llm"
)

// RunHeadlessSession runs a single multi-step "turn" (like the TUI) without any UI.
// It injects the given run_id into agent_* tool calls automatically and returns the
// final assistant content for the turn.
func (a *Agent) RunHeadlessSession(ctx context.Context, runID string, userText string, baseHistory []llm.Message) (string, error) {
	if a == nil {
		return "", errors.New("agent is nil")
	}
	final := ""
	emit := func(msg llm.Message) {
		if msg.Role == "assistant" && len(msg.ToolCalls) == 0 {
			final = msg.Content
		}
	}
	if err := runTUITurnStreaming(ctx, a, runID, userText, baseHistory, emit, nil); err != nil {
		return "", err
	}
	return strings.TrimSpace(final), nil
}
