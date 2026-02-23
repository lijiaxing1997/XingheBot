package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"test_skill_agent/internal/llm"
)

type fakeChatClient struct {
	responses []fakeChatResponse
}

type fakeChatResponse struct {
	resp *llm.ChatResponse
	err  error
}

func (f *fakeChatClient) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if len(f.responses) == 0 {
		return nil, errors.New("unexpected Chat call")
	}
	r := f.responses[0]
	f.responses = f.responses[1:]
	return r.resp, r.err
}

func TestPruneHistoryAfterLastAutoCompaction(t *testing.T) {
	h := []llm.Message{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
		{Role: "system", Content: "[System Message] Context compacted automatically due to context overflow.\n\nSummary..."},
		{Role: "user", Content: "c"},
	}
	got := pruneHistoryAfterLastAutoCompaction(h)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages after prune, got %d", len(got))
	}
	if got[0].Role != "system" {
		t.Fatalf("expected first pruned message to be system, got %q", got[0].Role)
	}
	if got[1].Content != "c" {
		t.Fatalf("expected last message content %q, got %q", "c", got[1].Content)
	}
}

func TestTruncateToolMessages(t *testing.T) {
	huge := strings.Repeat("x", 10_000)
	msgs := []llm.Message{
		{Role: "user", Content: "hi"},
		{Role: "tool", ToolCallID: "call_1", Content: huge},
	}
	got := truncateToolMessages(msgs, 2000, "test")
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[1].Role != "tool" {
		t.Fatalf("expected tool role, got %q", got[1].Role)
	}
	if len(got[1].Content) > 3000 {
		t.Fatalf("expected truncated content to be reasonably small, got %d bytes", len(got[1].Content))
	}
	if !strings.Contains(got[1].Content, "Tool result truncated") {
		t.Fatalf("expected truncation suffix, got: %q", got[1].Content[len(got[1].Content)-120:])
	}
}

func TestAutoCompactionConfigPatchApplyTo(t *testing.T) {
	base := DefaultAutoCompactionConfig()
	enabled := false
	maxAttempts := 0
	keepLastUserTurns := 1
	summaryMaxTokens := 256
	invalidNegative := -1

	patch := AutoCompactionConfigPatch{
		Enabled:           &enabled,
		MaxAttempts:       &maxAttempts,
		KeepLastUserTurns: &keepLastUserTurns,
		SummaryMaxTokens:  &summaryMaxTokens,
		SummaryMaxChars:   &invalidNegative, // ignored
	}

	got := patch.ApplyTo(base)
	if got.Enabled != false {
		t.Fatalf("expected enabled=false, got %v", got.Enabled)
	}
	if got.MaxAttempts != 0 {
		t.Fatalf("expected max_attempts=0, got %d", got.MaxAttempts)
	}
	if got.KeepLastUserTurns != 1 {
		t.Fatalf("expected keep_last_user_turns=1, got %d", got.KeepLastUserTurns)
	}
	if got.SummaryMaxTokens != 256 {
		t.Fatalf("expected summary_max_tokens=256, got %d", got.SummaryMaxTokens)
	}
	if got.SummaryMaxChars != base.SummaryMaxChars {
		t.Fatalf("expected invalid summary_max_chars to be ignored (keep %d), got %d", base.SummaryMaxChars, got.SummaryMaxChars)
	}
}

func TestCompactMessagesForOverflow_InsertsSummaryAndKeepsTail(t *testing.T) {
	fake := &fakeChatClient{
		responses: []fakeChatResponse{
			{
				resp: &llm.ChatResponse{
					Choices: []llm.Choice{{
						Message: llm.Message{Role: "assistant", Content: "- summary line 1\n- summary line 2"},
					}},
				},
			},
		},
	}

	cfg := DefaultAutoCompactionConfig()
	cfg.KeepLastUserTurns = 1
	cfg.OverflowMaxToolResultChars = 2000
	cfg.SummaryInputMaxChars = 4000
	cfg.SummaryMaxChars = 2000

	toolHuge := strings.Repeat("y", 50_000)
	input := []llm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "old user"},
		{Role: "assistant", Content: "old assistant"},
		{Role: "user", Content: "recent user"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      "list_files",
				Arguments: `{"path":"."}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call_1", Content: toolHuge},
		{Role: "assistant", Content: "recent assistant"},
	}

	got, summaryMsg, ok := compactMessagesForOverflow(context.Background(), fake, input, cfg)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if summaryMsg == nil {
		t.Fatalf("expected a summary message")
	}
	if len(got) < 4 {
		t.Fatalf("expected compacted messages, got %d", len(got))
	}
	if got[0].Role != "system" || got[0].Content != "sys" {
		t.Fatalf("expected to keep leading system preamble, got: %#v", got[0])
	}
	if got[1].Role != "system" || !strings.Contains(got[1].Content, "Context compacted automatically") {
		t.Fatalf("expected injected compaction system message, got: %#v", got[1])
	}
	if got[2].Role != "user" || got[2].Content != "recent user" {
		t.Fatalf("expected to keep tail starting at recent user, got: %#v", got[2])
	}
	foundTool := false
	for _, m := range got {
		if strings.EqualFold(m.Role, "tool") {
			foundTool = true
			if len(m.Content) > 4000 {
				t.Fatalf("expected tool content truncated in compacted history, got %d bytes", len(m.Content))
			}
		}
	}
	if !foundTool {
		t.Fatalf("expected tool message in tail to be kept")
	}
}
