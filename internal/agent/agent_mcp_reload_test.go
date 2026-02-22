package agent

import (
	"testing"

	"test_skill_agent/internal/llm"
)

func TestShouldTriggerNaturalLanguageMCPReload(t *testing.T) {
	a := &Agent{}

	trueCases := []string{
		"刷新mcp",
		"请重新加载mcp",
		"reload mcp",
		"mcp refresh",
		"can you reload mcp?",
	}
	for _, tc := range trueCases {
		if !a.shouldTriggerNaturalLanguageMCPReload(tc) {
			t.Fatalf("expected true for %q", tc)
		}
	}

	falseCases := []string{
		"mcp_reload 是自动的吗",
		"what is mcp reload",
		"如何配置 mcp",
		"list mcp tools",
	}
	for _, tc := range falseCases {
		if a.shouldTriggerNaturalLanguageMCPReload(tc) {
			t.Fatalf("expected false for %q", tc)
		}
	}
}

func TestIsMCPRelatedPath(t *testing.T) {
	trueCases := []string{
		"mcp/calculator/server.py",
		"./mcp/calculator/server.py",
		"/tmp/work/mcp/calculator/server.py",
		"mcp.json",
		"mcp.exm.json",
		"bin/calculator-mcp",
	}
	for _, tc := range trueCases {
		if !isMCPRelatedPath(tc) {
			t.Fatalf("expected true for path %q", tc)
		}
	}

	falseCases := []string{
		"README.md",
		"skills/mcp-builder/SKILL.md",
		"bin/XingheBot",
		"internal/agent/agent.go",
	}
	for _, tc := range falseCases {
		if isMCPRelatedPath(tc) {
			t.Fatalf("expected false for path %q", tc)
		}
	}
}

func TestShouldTriggerAutoMCPReloadAfterToolCall(t *testing.T) {
	a := &Agent{}

	cases := []struct {
		name     string
		call     llm.ToolCall
		callErr  error
		expected bool
	}{
		{
			name: "write mcp file",
			call: llm.ToolCall{
				Function: llm.ToolCallFunction{
					Name:      "write_file",
					Arguments: `{"path":"mcp/calculator/server.py","content":"x"}`,
				},
			},
			expected: true,
		},
		{
			name: "edit skill doc",
			call: llm.ToolCall{
				Function: llm.ToolCallFunction{
					Name:      "edit_file",
					Arguments: `{"path":"skills/mcp-builder/SKILL.md","edits":[{"old_text":"a","new_text":"b"}]}`,
				},
			},
			expected: false,
		},
		{
			name: "move to config",
			call: llm.ToolCall{
				Function: llm.ToolCallFunction{
					Name:      "move_file",
					Arguments: `{"src":"tmp/new.json","dest":"mcp.json"}`,
				},
			},
			expected: true,
		},
		{
			name: "explicit mcp_reload call",
			call: llm.ToolCall{
				Function: llm.ToolCallFunction{
					Name:      "mcp_reload",
					Arguments: `{}`,
				},
			},
			expected: false,
		},
		{
			name: "tool failed",
			call: llm.ToolCall{
				Function: llm.ToolCallFunction{
					Name:      "write_file",
					Arguments: `{"path":"mcp/a.py","content":"x"}`,
				},
			},
			callErr:  assertErr{},
			expected: false,
		},
	}

	for _, tc := range cases {
		got := a.shouldTriggerAutoMCPReloadAfterToolCall(tc.call, tc.callErr)
		if got != tc.expected {
			t.Fatalf("%s: expected %v, got %v", tc.name, tc.expected, got)
		}
	}
}

type assertErr struct{}

func (assertErr) Error() string { return "err" }
