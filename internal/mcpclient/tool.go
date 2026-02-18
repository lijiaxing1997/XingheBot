package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"test_skill_agent/internal/llm"
)

type MCPTool struct {
	ServerName  string
	LocalName   string
	RemoteName  string
	Description string
	InputSchema any
	Session     *mcp.ClientSession
}

func ToolsFromServers(servers []*Server) ([]*MCPTool, error) {
	tools := make([]*MCPTool, 0)
	used := make(map[string]bool)
	errs := make([]string, 0)

	for _, server := range servers {
		if server == nil {
			continue
		}
		serverName := strings.TrimSpace(server.Config.Name)
		for _, tool := range server.Tools {
			if tool == nil {
				continue
			}
			localName := makeLocalToolName(serverName, tool.Name)
			if localName == "" {
				errs = append(errs, fmt.Sprintf("%s: tool name is empty", serverName))
				continue
			}
			if used[localName] {
				errs = append(errs, fmt.Sprintf("duplicate tool name: %s", localName))
				continue
			}
			used[localName] = true
			tools = append(tools, newMCPTool(serverName, localName, tool, server.Session))
		}
	}

	if len(errs) > 0 {
		return tools, fmt.Errorf("mcp: %s", strings.Join(errs, "; "))
	}
	return tools, nil
}

func newMCPTool(serverName, localName string, tool *mcp.Tool, session *mcp.ClientSession) *MCPTool {
	desc := strings.TrimSpace(tool.Description)
	if desc == "" {
		desc = fmt.Sprintf("MCP tool from %s", serverName)
	} else {
		desc = fmt.Sprintf("[MCP:%s] %s", serverName, desc)
	}
	input := tool.InputSchema
	if input == nil {
		input = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return &MCPTool{
		ServerName:  serverName,
		LocalName:   localName,
		RemoteName:  tool.Name,
		Description: desc,
		InputSchema: input,
		Session:     session,
	}
}

func (t *MCPTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        t.LocalName,
			Description: t.Description,
			Parameters:  t.InputSchema,
		},
	}
}

func (t *MCPTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t == nil || t.Session == nil {
		return "", fmt.Errorf("mcp tool %s is not connected", t.LocalName)
	}

	var parsed any
	if len(args) > 0 {
		if err := json.Unmarshal(args, &parsed); err != nil {
			return "", err
		}
	}

	res, err := t.Session.CallTool(ctx, &mcp.CallToolParams{
		Name:      t.RemoteName,
		Arguments: parsed,
	})
	if err != nil {
		return "", err
	}

	return formatCallToolResult(res)
}

func formatCallToolResult(res *mcp.CallToolResult) (string, error) {
	if res == nil {
		return "", nil
	}
	if res.StructuredContent == nil && allTextContent(res.Content) {
		return joinText(res.Content), nil
	}
	data, err := json.Marshal(res)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func allTextContent(content []mcp.Content) bool {
	for _, item := range content {
		if _, ok := item.(*mcp.TextContent); !ok {
			return false
		}
	}
	return true
}

func joinText(content []mcp.Content) string {
	parts := make([]string, 0, len(content))
	for _, item := range content {
		if text, ok := item.(*mcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func makeLocalToolName(serverName, toolName string) string {
	server := sanitizeName(serverName)
	tool := sanitizeName(toolName)
	switch {
	case server == "" && tool == "":
		return ""
	case server == "":
		return tool
	case tool == "":
		return server
	default:
		return server + "__" + tool
	}
}

func sanitizeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
