package llm

import (
	"encoding/json"
	"strings"
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

func TestSanitizeToolCallArguments(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantValid bool
	}{
		{name: "valid_object", in: `{"path":"."}`, wantValid: true},
		{name: "empty", in: "", wantValid: true},
		{name: "invalid_json", in: `{"a":,}`, wantValid: true},
		{name: "non_object_json", in: `[]`, wantValid: true},
		{name: "whitespace", in: " \n\t ", wantValid: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeToolCallArguments(tc.in)
			if tc.wantValid && !json.Valid([]byte(got)) {
				t.Fatalf("expected valid JSON, got: %q", got)
			}
			if !strings.HasPrefix(strings.TrimSpace(got), "{") {
				t.Fatalf("expected JSON object, got: %q", got)
			}
		})
	}
}
