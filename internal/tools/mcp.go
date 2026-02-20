package tools

import (
	"context"
	"encoding/json"
	"errors"

	"test_skill_agent/internal/llm"
)

type MCPReloadTool struct {
	Reload func(ctx context.Context) (string, error)
}

func (t *MCPReloadTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "mcp_reload",
			Description: "Reload MCP servers from config and refresh MCP tools without restarting the agent process.",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}
}

func (t *MCPReloadTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if len(args) > 0 {
		var payload map[string]any
		if err := json.Unmarshal(args, &payload); err != nil {
			return "", err
		}
	}
	if t.Reload == nil {
		return "", errors.New("mcp reload is not configured")
	}
	return t.Reload(ctx)
}
