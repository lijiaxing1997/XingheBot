package agent

import (
	"testing"

	"test_skill_agent/internal/llm"
)

func TestHistoryHasUserMessages(t *testing.T) {
	if historyHasUserMessages(nil) {
		t.Fatalf("expected false for nil history")
	}
	if historyHasUserMessages([]llm.Message{{Role: "system", Content: "hi"}}) {
		t.Fatalf("expected false when only system messages exist")
	}
	if !historyHasUserMessages([]llm.Message{
		{Role: "system", Content: "notice"},
		{Role: "user", Content: "hello"},
	}) {
		t.Fatalf("expected true when user message exists")
	}
}

