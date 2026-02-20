package mcpclient

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type Runtime struct {
	mu         sync.RWMutex
	configPath string
	servers    []*Server
	tools      []*MCPTool
	toolNames  []string
}

type ReloadReport struct {
	ConfigPath string
	Servers    int
	Tools      int
	Warnings   []string
}

func NewRuntime(configPath string) *Runtime {
	return &Runtime{configPath: strings.TrimSpace(configPath)}
}

func (r *Runtime) Reload(ctx context.Context) (ReloadReport, error) {
	path := strings.TrimSpace(r.configPath)
	if path == "" {
		path = "mcp.json"
	}
	report := ReloadReport{ConfigPath: path}

	cfg, err := LoadConfig(path)
	if err != nil {
		return report, err
	}

	servers, connectErr := ConnectServers(ctx, cfg.Servers)
	tools, toolsErr := ToolsFromServers(servers)

	if len(cfg.Servers) > 0 && len(servers) == 0 {
		_ = CloseServers(servers)
		if connectErr != nil {
			return report, fmt.Errorf("mcp reload failed: no servers connected (%v)", connectErr)
		}
		return report, fmt.Errorf("mcp reload failed: no servers connected")
	}

	toolNames := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		toolNames = append(toolNames, tool.LocalName)
	}

	warnings := make([]string, 0, 3)
	if connectErr != nil {
		warnings = append(warnings, connectErr.Error())
	}
	if toolsErr != nil {
		warnings = append(warnings, toolsErr.Error())
	}

	r.mu.Lock()
	oldServers := r.servers
	r.servers = servers
	r.tools = tools
	r.toolNames = toolNames
	r.mu.Unlock()

	if err := CloseServers(oldServers); err != nil {
		warnings = append(warnings, fmt.Sprintf("close previous sessions: %v", err))
	}

	report.Servers = len(servers)
	report.Tools = len(tools)
	report.Warnings = warnings
	return report, nil
}

func (r *Runtime) Tools() []*MCPTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*MCPTool, len(r.tools))
	copy(out, r.tools)
	return out
}

func (r *Runtime) ToolNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.toolNames))
	copy(out, r.toolNames)
	return out
}

func (r *Runtime) Close() error {
	r.mu.Lock()
	oldServers := r.servers
	r.servers = nil
	r.tools = nil
	r.toolNames = nil
	r.mu.Unlock()
	return CloseServers(oldServers)
}

func (r ReloadReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "mcp reload complete: config=%s servers=%d tools=%d", r.ConfigPath, r.Servers, r.Tools)
	if len(r.Warnings) > 0 {
		b.WriteString("\nwarnings:")
		for _, warn := range r.Warnings {
			b.WriteString("\n- ")
			b.WriteString(warn)
		}
	}
	return b.String()
}
