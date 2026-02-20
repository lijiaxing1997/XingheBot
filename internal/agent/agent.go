package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/skills"
	"test_skill_agent/internal/tools"
)

type Agent struct {
	Client       *llm.Client
	Tools        *tools.Registry
	SkillsDir    string
	SkillIndex   []skills.Skill
	MCPReload    func(context.Context) (string, error)
	Temperature  float32
	SystemPrompt string
	PromptMode   PromptMode
}

type PromptMode string

const (
	PromptModeChat   PromptMode = "chat"
	PromptModeWorker PromptMode = "worker"
)

type TaskHooks struct {
	BeforeModelCall func(ctx context.Context) ([]llm.Message, error)
	BeforeToolCall  func(ctx context.Context, name string, arguments string) error
	AfterToolCall   func(ctx context.Context, name string, arguments string, result string, callErr error, duration time.Duration) error
}

type TaskOptions struct {
	MaxTurns int
	Hooks    TaskHooks
}

func New(client *llm.Client, registry *tools.Registry, skillsDir string) (*Agent, error) {
	a := &Agent{
		Client:    client,
		Tools:     registry,
		SkillsDir: skillsDir,
		PromptMode: PromptModeChat,
	}
	if err := a.ReloadSkills(); err != nil {
		return nil, err
	}
	a.SystemPrompt = a.buildSystemPrompt()
	return a, nil
}

func (a *Agent) SetPromptMode(mode PromptMode) {
	if a == nil {
		return
	}
	if strings.TrimSpace(string(mode)) == "" {
		mode = PromptModeChat
	}
	a.PromptMode = mode
	a.SystemPrompt = a.buildSystemPrompt()
}

func (a *Agent) ReloadSkills() error {
	list, err := skills.LoadSkills(a.SkillsDir)
	if err != nil {
		return err
	}
	a.SkillIndex = list
	return nil
}

func (a *Agent) buildSystemPrompt() string {
	var b strings.Builder
	b.WriteString("You are a local coding agent with tool access. Use tools for filesystem operations instead of guessing.\n")
	b.WriteString("When calling write_file: arguments MUST be valid JSON (no raw code outside JSON). For large files, write in multiple calls with append=true after the first chunk and keep each call small (aim: args <= 6000 bytes) to avoid truncation.\n")
	b.WriteString("When the user requests skill management, use skill_create or skill_install.\n")
	b.WriteString("When MCP config/server setup changes at runtime, use mcp_reload to refresh MCP tools without restarting the agent.\n")
	b.WriteString("If the user asks to refresh/reload MCP in natural language, execute mcp_reload automatically.\n")
	b.WriteString("For complex tasks, you may create parallel child agents with agent_run_create + agent_spawn, and coordinate using agent_wait, agent_control, agent_signal_send, and agent_signal_wait.\n")
	switch a.PromptMode {
	case PromptModeWorker:
		b.WriteString("You are running as a CHILD worker agent. Focus on completing the assigned task. You may receive additional operator messages mid-run; treat them as updated requirements.\n")
	default:
		b.WriteString("You are running as the PRIMARY (gateway) agent in chat mode. Prefer asynchronous multi-agent execution: after planning + spawning child agents, return control to the user instead of blocking. Avoid calling agent_wait unless the user explicitly asks to wait; prefer agent_state / agent_run_list / agent_inspect for progress.\n")
		b.WriteString("To guide a child agent mid-run, use agent_control with command=\"message\" and payload like {\"text\":\"...\",\"role\":\"user\"}.\n")
	}
	b.WriteString("If a user mentions a skill name or uses $SkillName, load it with skill_load before proceeding.\n")
	if len(a.SkillIndex) == 0 {
		b.WriteString("Available skills: (none).\n")
		return b.String()
	}
	b.WriteString("Available skills:\n")
	for _, s := range a.SkillIndex {
		if s.Description != "" {
			b.WriteString(fmt.Sprintf("- %s: %s\n", s.Name, s.Description))
		} else {
			b.WriteString(fmt.Sprintf("- %s\n", s.Name))
		}
	}
	return b.String()
}

func (a *Agent) RunInteractive(ctx context.Context, in io.Reader, out io.Writer) error {
	if a.Client == nil {
		return fmt.Errorf("llm client is nil")
	}
	systemMsg := llm.Message{Role: "system", Content: a.SystemPrompt}
	history := make([]llm.Message, 0, 64)
	scanner := bufio.NewScanner(in)
	printer := newToolPrinter(out)

	for {
		fmt.Fprint(out, "> ")
		if !scanner.Scan() {
			return scanner.Err()
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		if text == "/exit" || text == "/quit" {
			return nil
		}
		if text == "/mcp reload" || text == "/mcp-reload" {
			msg, err := a.reloadMCP(ctx)
			if err != nil {
				fmt.Fprintf(out, "MCP reload failed: %v\n", err)
				continue
			}
			fmt.Fprintln(out, msg)
			continue
		}
		if a.shouldTriggerNaturalLanguageMCPReload(text) {
			msg, err := a.reloadMCP(ctx)
			if err != nil {
				fmt.Fprintf(out, "MCP reload failed: %v\n", err)
			} else {
				fmt.Fprintln(out, msg)
			}
			continue
		}

		skillMsgs := a.skillMessagesForInput(text)

		turnHistory := []llm.Message{{Role: "user", Content: text}}
		reqMessages := append([]llm.Message{}, systemMsg)
		reqMessages = append(reqMessages, history...)
		reqMessages = append(reqMessages, skillMsgs...)
		reqMessages = append(reqMessages, turnHistory...)

		for {
			resp, err := a.Client.Chat(ctx, llm.ChatRequest{
				Messages:    reqMessages,
				Tools:       a.Tools.Definitions(),
				Temperature: a.Temperature,
			})
			if err != nil {
				return err
			}
			msg := resp.Choices[0].Message
			turnHistory = append(turnHistory, msg)
			reqMessages = append(reqMessages, msg)

			if len(msg.ToolCalls) == 0 {
				if msg.Content != "" {
					fmt.Fprintln(out, msg.Content)
				}
				history = append(history, turnHistory...)
				break
			}

			needsAutoMCPReload := false
			for _, call := range msg.ToolCalls {
				printer.printToolCall(call.Function.Name, call.Function.Arguments)
				start := time.Now()
				result, err := a.callTool(ctx, call)
				printer.printToolResult(call.Function.Name, result, err, time.Since(start))
				toolMsg := llm.Message{
					Role:       "tool",
					ToolCallID: call.ID,
					Content:    result,
				}
				if err != nil {
					toolMsg.Content = "ERROR: " + err.Error()
				}
				turnHistory = append(turnHistory, toolMsg)
				reqMessages = append(reqMessages, toolMsg)
				if a.shouldTriggerAutoMCPReloadAfterToolCall(call, err) {
					needsAutoMCPReload = true
				}

				if call.Function.Name == "skill_create" || call.Function.Name == "skill_install" {
					_ = a.ReloadSkills()
					a.SystemPrompt = a.buildSystemPrompt()
					systemMsg = llm.Message{Role: "system", Content: a.SystemPrompt}
				}
			}

			if needsAutoMCPReload {
				msg, err := a.reloadMCP(ctx)
				contextMsg := ""
				if err != nil {
					contextMsg = fmt.Sprintf("System event: MCP auto-reload failed after MCP-related updates: %v", err)
					fmt.Fprintf(out, "MCP auto-reload failed: %v\n", err)
				} else {
					contextMsg = "System event: MCP auto-reload completed after MCP-related updates.\n" + msg
					fmt.Fprintln(out, "MCP auto-reload:", msg)
				}
				if strings.TrimSpace(contextMsg) != "" {
					autoMsg := llm.Message{Role: "system", Content: contextMsg}
					turnHistory = append(turnHistory, autoMsg)
					reqMessages = append(reqMessages, autoMsg)
				}
			}
		}
	}
}

func (a *Agent) callTool(ctx context.Context, call llm.ToolCall) (string, error) {
	args := json.RawMessage(call.Function.Arguments)
	if a.PromptMode == PromptModeChat {
		switch strings.TrimSpace(call.Function.Name) {
		case "agent_wait", "agent_signal_wait":
			args = clampTimeoutSeconds(args, 2, 15)
		}
	}
	return a.Tools.Call(ctx, call.Function.Name, args)
}

func clampTimeoutSeconds(raw json.RawMessage, defaultSeconds int, maxSeconds int) json.RawMessage {
	if defaultSeconds <= 0 {
		defaultSeconds = 2
	}
	if maxSeconds <= 0 {
		maxSeconds = 15
	}

	text := strings.TrimSpace(string(raw))
	if text == "" {
		return raw
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw
	}
	if obj == nil {
		return raw
	}

	timeout := 0
	if v, ok := obj["timeout_seconds"]; ok {
		if n, ok := v.(float64); ok {
			timeout = int(n)
		}
	}
	switch {
	case timeout <= 0:
		obj["timeout_seconds"] = defaultSeconds
	case timeout > maxSeconds:
		obj["timeout_seconds"] = maxSeconds
	default:
		return raw
	}

	out, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return out
}

func (a *Agent) RunTask(ctx context.Context, task string, opts TaskOptions) (string, error) {
	if a.Client == nil {
		return "", fmt.Errorf("llm client is nil")
	}
	task = strings.TrimSpace(task)
	if task == "" {
		return "", fmt.Errorf("task is required")
	}
	maxTurns := opts.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 40
	}

	systemMsg := llm.Message{Role: "system", Content: a.SystemPrompt}
	reqMessages := []llm.Message{systemMsg}
	reqMessages = append(reqMessages, a.skillMessagesForInput(task)...)
	reqMessages = append(reqMessages, llm.Message{Role: "user", Content: task})

	for turn := 0; turn < maxTurns; turn++ {
		if opts.Hooks.BeforeModelCall != nil {
			injected, err := opts.Hooks.BeforeModelCall(ctx)
			if err != nil {
				return "", err
			}
			if len(injected) > 0 {
				reqMessages = append(reqMessages, injected...)
			}
		}

		resp, err := a.Client.Chat(ctx, llm.ChatRequest{
			Messages:    reqMessages,
			Tools:       a.Tools.Definitions(),
			Temperature: a.Temperature,
		})
		if err != nil {
			return "", err
		}
		msg := resp.Choices[0].Message
		reqMessages = append(reqMessages, msg)

		if len(msg.ToolCalls) == 0 {
			return msg.Content, nil
		}

		needsAutoMCPReload := false
		for _, call := range msg.ToolCalls {
			if opts.Hooks.BeforeToolCall != nil {
				if err := opts.Hooks.BeforeToolCall(ctx, call.Function.Name, call.Function.Arguments); err != nil {
					return "", err
				}
			}

			start := time.Now()
			result, callErr := a.callTool(ctx, call)
			duration := time.Since(start)

			if opts.Hooks.AfterToolCall != nil {
				if err := opts.Hooks.AfterToolCall(ctx, call.Function.Name, call.Function.Arguments, result, callErr, duration); err != nil {
					return "", err
				}
			}

			toolMsg := llm.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    result,
			}
			if callErr != nil {
				toolMsg.Content = "ERROR: " + callErr.Error()
			}
			reqMessages = append(reqMessages, toolMsg)

			if a.shouldTriggerAutoMCPReloadAfterToolCall(call, callErr) {
				needsAutoMCPReload = true
			}
			if call.Function.Name == "skill_create" || call.Function.Name == "skill_install" {
				_ = a.ReloadSkills()
				a.SystemPrompt = a.buildSystemPrompt()
				systemMsg = llm.Message{Role: "system", Content: a.SystemPrompt}
				reqMessages = append(reqMessages, systemMsg)
			}
		}

		if needsAutoMCPReload {
			msg, err := a.reloadMCP(ctx)
			contextMsg := ""
			if err != nil {
				contextMsg = fmt.Sprintf("System event: MCP auto-reload failed after MCP-related updates: %v", err)
			} else {
				contextMsg = "System event: MCP auto-reload completed after MCP-related updates.\n" + msg
			}
			if strings.TrimSpace(contextMsg) != "" {
				reqMessages = append(reqMessages, llm.Message{Role: "system", Content: contextMsg})
			}
		}
	}

	return "", fmt.Errorf("task reached max turns: %d", maxTurns)
}

func (a *Agent) skillMessagesForInput(input string) []llm.Message {
	if len(a.SkillIndex) == 0 {
		return nil
	}
	lower := strings.ToLower(input)
	msgs := make([]llm.Message, 0, 2)
	for _, s := range a.SkillIndex {
		nameLower := strings.ToLower(s.Name)
		if strings.Contains(lower, "$"+nameLower) || strings.Contains(lower, nameLower) {
			body, err := skills.LoadSkillBody(s.SkillPath)
			if err != nil {
				continue
			}
			content := fmt.Sprintf("Skill: %s\n%s", s.Name, body)
			msgs = append(msgs, llm.Message{Role: "system", Content: content})
		}
	}
	return msgs
}

func (a *Agent) reloadMCP(ctx context.Context) (string, error) {
	if a.MCPReload == nil {
		return "", fmt.Errorf("MCP reload is not configured")
	}
	msg, err := a.MCPReload(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(msg) == "" {
		msg = "mcp reload complete"
	}
	return msg, nil
}

func (a *Agent) shouldTriggerNaturalLanguageMCPReload(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "mcp_reload") {
		// Usually discussion about the tool name itself, not an execution intent.
		return false
	}

	compact := strings.ReplaceAll(lower, " ", "")
	compact = strings.ReplaceAll(compact, "\t", "")

	phrases := []string{
		"reload mcp",
		"refresh mcp",
		"mcp reload",
		"mcp refresh",
		"reconnect mcp",
		"刷新mcp",
		"重载mcp",
		"重新加载mcp",
		"重新载入mcp",
		"重新连接mcp",
		"mcp刷新",
		"mcp重载",
		"mcp重新加载",
		"mcpreload",
		"mcprefresh",
	}

	hasReloadPhrase := false
	for _, phrase := range phrases {
		if strings.Contains(lower, phrase) || strings.Contains(compact, phrase) {
			hasReloadPhrase = true
			break
		}
	}
	if !hasReloadPhrase {
		return false
	}

	questionHints := []string{"?", "？", "什么", "怎么", "如何", "why", "what", "when", "是否", "是不是", "吗", "自动", "manual", "手动"}
	if containsAny(lower, questionHints) {
		requestHints := []string{"请", "帮我", "帮忙", "麻烦", "执行", "触发", "please", "can you", "could you", "now", "立即", "立刻", "马上"}
		return containsAny(lower, requestHints)
	}

	return true
}

func (a *Agent) shouldTriggerAutoMCPReloadAfterToolCall(call llm.ToolCall, callErr error) bool {
	if callErr != nil {
		return false
	}
	name := strings.TrimSpace(call.Function.Name)
	if name == "" || name == "mcp_reload" {
		return false
	}

	args := parseToolArgs(call.Function.Arguments)
	switch name {
	case "write_file", "edit_file", "delete_file":
		return isMCPRelatedPath(argString(args, "path"))
	case "move_file", "copy_file":
		return isMCPRelatedPath(argString(args, "src")) || isMCPRelatedPath(argString(args, "dest"))
	default:
		return false
	}
}

func parseToolArgs(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func argString(args map[string]any, key string) string {
	if len(args) == 0 {
		return ""
	}
	v, ok := args[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func isMCPRelatedPath(path string) bool {
	p := strings.TrimSpace(path)
	if p == "" {
		return false
	}
	p = strings.Trim(p, "\"'")
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "./")
	lp := strings.ToLower(p)

	if strings.HasPrefix(lp, "mcp/") || strings.Contains(lp, "/mcp/") {
		return true
	}

	base := strings.ToLower(filepath.Base(lp))
	if base == "mcp.json" || base == "mcp.exm.json" {
		return true
	}

	if strings.HasPrefix(lp, "bin/") && strings.Contains(base, "mcp") {
		return true
	}

	return false
}

func containsAny(s string, needles []string) bool {
	for _, needle := range needles {
		if needle == "" {
			continue
		}
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
