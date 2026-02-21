package llm

import (
	"encoding/json"
	"testing"
)

func TestChatRequestMarshal_IncludesEmptyAssistantContent(t *testing.T) {
	req := ChatRequest{
		Model: "gpt-4o-mini",
		Messages: []Message{
			{Role: "system", Content: "sys"},
			{
				Role:    "assistant",
				Content: "",
				ToolCalls: []ToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: ToolCallFunction{
						Name:      "list_files",
						Arguments: `{"path":"."}`,
					},
				}},
			},
		},
	}

	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	msgsAny, ok := decoded["messages"].([]any)
	if !ok || len(msgsAny) != 2 {
		t.Fatalf("expected 2 messages, got: %#v", decoded["messages"])
	}

	assistantAny, ok := msgsAny[1].(map[string]any)
	if !ok {
		t.Fatalf("expected assistant message to be an object, got: %#v", msgsAny[1])
	}
	if _, ok := assistantAny["content"]; !ok {
		t.Fatalf("expected assistant message to include content field, got: %s", string(raw))
	}
	if got, _ := assistantAny["content"].(string); got != "" {
		t.Fatalf("expected empty content string, got: %#v", assistantAny["content"])
	}
}

