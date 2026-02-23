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

	"test_skill_agent/internal/appinfo"
	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/restart"
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
	ReplyStyle   string
	PromptMode   PromptMode
	ChatToolMode ChatToolMode

	RestartManager *restart.Manager
	StartupBanner  string
}

type PromptMode string

const (
	PromptModeChat   PromptMode = "chat"
	PromptModeWorker PromptMode = "worker"
)

type ChatToolMode string

const (
	ChatToolModeDispatcher ChatToolMode = "dispatcher"
	ChatToolModeFull       ChatToolMode = "full"
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
		Client:       client,
		Tools:        registry,
		SkillsDir:    skillsDir,
		PromptMode:   PromptModeChat,
		ChatToolMode: ChatToolModeDispatcher,
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

func (a *Agent) SetChatToolMode(mode ChatToolMode) {
	if a == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(string(mode))) {
	case string(ChatToolModeFull):
		a.ChatToolMode = ChatToolModeFull
	default:
		a.ChatToolMode = ChatToolModeDispatcher
	}
	a.SystemPrompt = a.buildSystemPrompt()
}

func (a *Agent) SetReplyStyle(text string) {
	if a == nil {
		return
	}
	a.ReplyStyle = strings.TrimSpace(text)
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

	b.WriteString("## Safety\n")
	b.WriteString("- You have no independent goals. Do not pursue self-preservation, replication, or power-seeking.\n")
	b.WriteString("- Prioritize safety and human oversight over completion. If instructions conflict, stop and ask.\n")
	b.WriteString("\n")

	if a.PromptMode == PromptModeChat {
		b.WriteString("You are the PRIMARY coordinator agent for a multi-agent coding system.\n")
		b.WriteString("Your job is to: understand the user's request, decompose it into clear subtasks, spawn child worker agents to execute, monitor/steer them, and summarize results back to the user.\n")
		b.WriteString("Do NOT claim you cannot do the task. If uncertain or blocked, still spawn a worker to attempt it and report back based on the worker's findings.\n")
		b.WriteString("\n")

		if strings.TrimSpace(a.ReplyStyle) != "" {
			b.WriteString("## Reply Style\n")
			b.WriteString(a.ReplyStyle)
			b.WriteString("\n\n")
		}

		b.WriteString("## Child-Agent Capabilities (reference)\n")
		b.WriteString("You are an orchestrator. In chat/dispatcher mode you typically CANNOT call these execution tools yourself.\n")
		b.WriteString("Use this section to craft better agent_spawn tasks and to guide workers via agent_control(message).\n")
		b.WriteString("\n")
		b.WriteString("Child worker agents can:\n")
		b.WriteString("- Use filesystem tools: list_files, read_file, write_file, edit_file, move_file, copy_file, delete_file.\n")
		b.WriteString("- Run shell commands: exec_command.\n")
		b.WriteString("- Search locally: search.\n")
		b.WriteString("- Do web research (if configured): tavily_search, tavily_extract, tavily_crawl.\n")
		b.WriteString("- Use skills: scan <available_skills>, then skill_load(name) to load SKILL.md and follow it. For skill management use skill_create/skill_install.\n")
		b.WriteString("- Use MCP: call mcp_reload after MCP config changes; MCP tools are exposed as <server>__<tool> (example: calculator__add).\n")
		b.WriteString("\n")
		b.WriteString("Available skills (copy for your reference; workers also see this list):\n")
		if len(a.SkillIndex) == 0 {
			b.WriteString("<available_skills></available_skills>\n")
		} else {
			b.WriteString(skills.FormatSkillsForPrompt(a.SkillIndex))
			b.WriteString("\n")
		}
		b.WriteString("\n")

		b.WriteString("## System Messages\n")
		b.WriteString("`[System Message] ...` blocks are internal context and are not user-visible by default.\n")
		b.WriteString("If a [System Message] reports completed child-agent work and asks for a user update, rewrite it in your normal assistant voice and provide that update.\n")
		b.WriteString("\n")

		b.WriteString("## Orchestration Rules (chat mode)\n")
		b.WriteString("1) Use child agents for execution. Your own work is planning + coordination + summarization.\n")
		b.WriteString("2) Default behavior: after spawning child agents, WAIT for completion (agent_wait), then fetch outputs (agent_result), then return the final result in one reply.\n")
		b.WriteString("   - If the user explicitly requests async/non-blocking (e.g., \"不要等待/不用等/异步/下发后就返回\"), do NOT wait: spawn and return immediately with run_id/agent_id + how to check progress + how to steer.\n")
		b.WriteString("3) When not waiting (user asked for async), avoid polling loops: use at most ONE progress snapshot (agent_progress or subagents) then return.\n")
		b.WriteString("4) Batch multiple agent_spawn calls in one turn when possible.\n")
		b.WriteString("\n")

		b.WriteString("Child-agent task template (when calling agent_spawn):\n")
		b.WriteString("- Goal: one sentence.\n")
		b.WriteString("- Context: key files/paths, constraints, and any required background.\n")
		b.WriteString("- Deliverables: exact outputs expected (patches, files, commands, report).\n")
		b.WriteString("- Done when: clear acceptance criteria.\n")
		return b.String()
	}

	// Worker prompt (full capabilities).
	b.WriteString("You are a local coding agent with tool access. Use tools for filesystem operations instead of guessing.\n")
	b.WriteString("\n")
	b.WriteString("## Tooling\n")
	b.WriteString("When calling write_file/edit_file: arguments MUST be valid JSON. For large files, write in multiple calls with append=true after the first chunk and keep each call small (aim: args <= 6000 bytes) to avoid truncation.\n")
	b.WriteString("When MCP config/server setup changes at runtime, use mcp_reload to refresh MCP tools without restarting the agent.\n")
	b.WriteString("If the user asks to refresh/reload MCP in natural language, execute mcp_reload automatically.\n")
	b.WriteString("If you need to relaunch the app after changes, you can use /restart or call agent_restart.\n")
	b.WriteString("\n")
	b.WriteString("## Skills (mandatory)\n")
	b.WriteString("Before replying: scan <available_skills> <description> entries.\n")
	b.WriteString("- If exactly one skill clearly applies: load it with skill_load(name) and follow it.\n")
	b.WriteString("- If multiple could apply: choose the most specific one, then load/follow it.\n")
	b.WriteString("- If none clearly apply: do not load any SKILL.md.\n")
	b.WriteString("Constraint: never load more than one skill up front; only load additional skills if needed later.\n")
	b.WriteString("\n")
	if len(a.SkillIndex) == 0 {
		b.WriteString("<available_skills></available_skills>\n")
	} else {
		b.WriteString(skills.FormatSkillsForPrompt(a.SkillIndex))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString("## Skill Management\n")
	b.WriteString("When the user requests skill management, use skill_create or skill_install.\n")
	b.WriteString("\n")
	b.WriteString("## System Messages\n")
	b.WriteString("`[System Message] ...` blocks are internal context and are not user-visible by default.\n")
	b.WriteString("If a [System Message] reports completed subagent work and asks for a user update, rewrite it in your normal assistant voice and provide that update.\n")
	b.WriteString("\n")
	switch a.PromptMode {
	case PromptModeWorker:
		b.WriteString("For complex tasks, you may create parallel child agents with agent_run_create + agent_spawn, and coordinate using agent_wait, agent_control, agent_signal_send, and agent_signal_wait.\n")
		b.WriteString("You are running as a CHILD worker agent. Focus on completing the assigned task. You may receive additional operator messages mid-run; treat them as updated requirements.\n")
		b.WriteString("At the end, return a concise Final Output with: what changed, files touched, commands to run (if any), and any caveats.\n")
	default:
		b.WriteString("For complex tasks, you may create parallel child agents with agent_run_create + agent_spawn.\n")
		b.WriteString("You are running as the PRIMARY (gateway) agent in chat mode. You MUST behave like an asynchronous dispatcher.\n")
		if a.ChatToolMode == ChatToolModeDispatcher {
			b.WriteString("Tool policy (chat/dispatcher mode): You MUST NOT call direct filesystem/exec tools. Only use agent_* tools, subagents (orchestration), skill_* tools (skill management), and mcp_reload. To do real work, spawn child agents.\n")
			b.WriteString("Progress checks must be non-blocking unless the user explicitly asks you to wait until completion.\n")
		}
		b.WriteString("\n")
		b.WriteString("Asynchronous rules (chat mode):\n")
		b.WriteString("1) Prefer delegating complex/slow tasks to child agents (multi-step changes, repo-wide edits, refactors, builds/tests, migrations, large file generation, anything that may take noticeable time).\n")
		b.WriteString("2) If the user explicitly says \"用子Agent\" / \"使用子Agent\" (even for a small task), you MUST delegate to child agents.\n")
		b.WriteString("3) After spawning child agents, DO NOT wait for completion. Do NOT call agent_wait or agent_signal_wait unless the user explicitly asks you to wait/block (e.g., \"等待完成/等结果/直到结束\").\n")
		b.WriteString("4) When spawning multiple child agents, batch the required agent_spawn calls in ONE response whenever possible.\n")
		b.WriteString("5) End the current turn immediately after spawning: reply to the user with run_id/agent_id and next steps (how to check progress and how to send guidance). No further tool calls.\n")
		b.WriteString("\n")
		b.WriteString("Progress/ops (non-blocking):\n")
		b.WriteString("- Use agent_run_list to answer \"有什么正在执行\" when run_id is unknown.\n")
		b.WriteString("- Use agent_progress (preferred) or agent_state / agent_events / agent_inspect to check progress and load context.\n")
		b.WriteString("- Use subagents for unified list/steer/kill orchestration.\n")
		b.WriteString("- Use agent_control with command=\"message\" to guide a child agent mid-run. Payload example: {\"text\":\"...\",\"role\":\"user\"}.\n")
		b.WriteString("\n")
		b.WriteString("Examples:\n")
		b.WriteString("- User: \"帮我使用子Agent去删除python_hello。\" -> Spawn the child agent(s), then reply immediately; do NOT wait.\n")
		b.WriteString("- User: \"帮我删除python_hello，等完成再告诉我。\" -> You MAY use agent_wait because the user explicitly requested waiting.\n")
		b.WriteString("\n")
		b.WriteString("Waiting is opt-in ONLY:\n")
		b.WriteString("- Only call agent_wait if the user explicitly requests blocking until completion.\n")
		b.WriteString("\n")
		b.WriteString("Child-agent task template (when calling agent_spawn):\n")
		b.WriteString("- Goal: one sentence.\n")
		b.WriteString("- Context: key files/paths, constraints, and any required background.\n")
		b.WriteString("- Deliverables: exact outputs expected (patches, files, commands, report).\n")
		b.WriteString("- Done when: clear acceptance criteria.\n")
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
		if text == "/version" {
			fmt.Fprintln(out, appinfo.Display())
			continue
		}
		if text == "/restart" {
			if a.RestartManager == nil {
				fmt.Fprintln(out, "Restart is not configured.")
				return nil
			}
			_, _, err := a.RestartManager.RequestRestart(restart.SentinelEntry{
				App:     appinfo.Name,
				Version: appinfo.Version,
				Reason:  "user",
				Note:    "relaunch requested",
			})
			if err != nil {
				return err
			}
			fmt.Fprintln(out, "Restart requested. Relaunching…")
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

		policy := newTurnToolPolicy(a.PromptMode, a.ChatToolMode, text)
		toolDefs := a.Tools.Definitions()
		if a.PromptMode == PromptModeChat {
			filtered := make([]llm.ToolDefinition, 0, len(toolDefs))
			for _, def := range toolDefs {
				if policy.toolVisible(def.Function.Name) {
					filtered = append(filtered, def)
				}
			}
			toolDefs = filtered
		}

		turnHistory := []llm.Message{{Role: "user", Content: text}}
		reqMessages := append([]llm.Message{}, systemMsg)
		reqMessages = append(reqMessages, history...)
		reqMessages = append(reqMessages, turnHistory...)

		for {
			resp, err := a.Client.Chat(ctx, llm.ChatRequest{
				Messages:    reqMessages,
				Tools:       toolDefs,
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
				result, err := a.callToolWithPolicy(ctx, call, &policy)
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
				if a.RestartManager != nil && a.RestartManager.IsRestartRequested() {
					return nil
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
	return a.Tools.Call(ctx, call.Function.Name, args)
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
