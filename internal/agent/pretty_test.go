package agent

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestPrintToolCall_MCPIncludesServerAndToolSummary(t *testing.T) {
	var out bytes.Buffer
	p := toolPrinter{out: &out, color: false}

	p.printToolCall("calculator__evaluate", `{"b":2,"a":1}`)
	got := out.String()

	if !strings.Contains(got, "mcp_server: calculator") {
		t.Fatalf("expected mcp_server summary, got:\n%s", got)
	}
	if !strings.Contains(got, "mcp_tool: evaluate") {
		t.Fatalf("expected mcp_tool summary, got:\n%s", got)
	}
	if !strings.Contains(got, "arg_keys: a, b") {
		t.Fatalf("expected sorted arg keys, got:\n%s", got)
	}
}

func TestPrintToolResult_MCPStructuredOutput(t *testing.T) {
	var out bytes.Buffer
	p := toolPrinter{out: &out, color: false}
	result := `{"content":[{"type":"text","text":"hello"},{"type":"text","text":"world"}],"structuredContent":{"ok":true},"isError":false}`

	p.printToolResult("calculator__evaluate", result, nil, 8*time.Millisecond)
	got := out.String()

	checks := []string{
		"status: ok",
		"mcp_server: calculator",
		"mcp_tool: evaluate",
		"mcp_is_error: false",
		"mcp_content_items: 2",
		"mcp_text:",
		"hello",
		"world",
		"mcp_structured:",
		"\"ok\": true",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in output, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "output:") {
		t.Fatalf("did not expect generic output block for parsed MCP result, got:\n%s", got)
	}
}

func TestParseMCPResult_NonMCPJSON(t *testing.T) {
	if _, ok := parseMCPResult(`{"foo":"bar"}`); ok {
		t.Fatalf("expected non-MCP JSON to return ok=false")
	}
}
