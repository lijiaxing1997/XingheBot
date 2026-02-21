package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"test_skill_agent/internal/agent"
	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/mcpclient"
	"test_skill_agent/internal/multiagent"
	"test_skill_agent/internal/skills"
	"test_skill_agent/internal/tools"
)

type runtimeOptions struct {
	SkillsDir      string
	Temperature    float64
	MaxTokens      int
	ConfigPath     string
	MCPConfigPath  string
	MultiAgentRoot string
}

type agentRuntime struct {
	Client      *llm.Client
	Registry    *tools.Registry
	Coordinator *multiagent.Coordinator
	ReloadMCP   func(context.Context) (string, error)
	closeFn     func() error
}

func (r *agentRuntime) Close() error {
	if r == nil || r.closeFn == nil {
		return nil
	}
	return r.closeFn()
}

func main() {
	if len(os.Args) < 2 {
		if err := runChat(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	switch os.Args[1] {
	case "chat":
		if err := runChat(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "worker":
		if err := runWorker(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "skills":
		if err := runSkills(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	default:
		if err := runChat(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	}
}

func runChat(args []string) error {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	skillsDir := fs.String("skills-dir", defaultSkillsDir(), "skills directory")
	temperature := fs.Float64("temperature", 0.2, "LLM temperature")
	maxTokens := fs.Int("max-tokens", 0, "max tokens for completion (overrides config)")
	chatToolMode := fs.String("chat-tool-mode", "dispatcher", "chat tool access: dispatcher (agent_* only) or full")
	uiMode := fs.String("ui", "tui", "ui mode: tui (default) or plain")
	configPath := fs.String("config", "config.json", "path to config.json")
	mcpConfigPath := fs.String("mcp-config", "mcp.json", "path to MCP config")
	multiAgentRoot := fs.String("multi-agent-root", ".multi_agent/runs", "path to multi-agent run storage")
	fs.Parse(args)

	rt, err := newAgentRuntime(runtimeOptions{
		SkillsDir:      *skillsDir,
		Temperature:    *temperature,
		MaxTokens:      *maxTokens,
		ConfigPath:     *configPath,
		MCPConfigPath:  *mcpConfigPath,
		MultiAgentRoot: *multiAgentRoot,
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := rt.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "warning:", err)
		}
	}()

	ag, err := agent.New(rt.Client, rt.Registry, *skillsDir)
	if err != nil {
		return err
	}
	ag.SetPromptMode(agent.PromptModeChat)
	ag.SetChatToolMode(agent.ChatToolMode(*chatToolMode))
	ag.Temperature = float32(*temperature)
	ag.MCPReload = rt.ReloadMCP

	switch strings.ToLower(strings.TrimSpace(*uiMode)) {
	case "", "tui":
		return ag.RunInteractiveTUI(context.Background(), os.Stdin, os.Stdout, agent.TUIOptions{
			Coordinator: rt.Coordinator,
		})
	default:
		fmt.Println("Agent ready. Type /mcp reload to refresh MCP servers, or /exit to quit.")
		return ag.RunInteractive(context.Background(), os.Stdin, os.Stdout)
	}
}

func runWorker(args []string) error {
	fs := flag.NewFlagSet("worker", flag.ExitOnError)
	skillsDir := fs.String("skills-dir", defaultSkillsDir(), "skills directory")
	temperature := fs.Float64("temperature", 0.2, "default worker temperature")
	maxTokens := fs.Int("max-tokens", 0, "default worker max tokens")
	configPath := fs.String("config", "config.json", "path to config.json")
	mcpConfigPath := fs.String("mcp-config", "mcp.json", "path to MCP config")
	multiAgentRoot := fs.String("run-root", ".multi_agent/runs", "path to multi-agent run storage")
	runID := fs.String("run-id", "", "run id")
	agentID := fs.String("agent-id", "", "agent id")
	fs.Parse(args)

	if strings.TrimSpace(*runID) == "" || strings.TrimSpace(*agentID) == "" {
		return fmt.Errorf("--run-id and --agent-id are required")
	}

	coord := multiagent.NewCoordinator(*multiAgentRoot)
	ctl := multiagent.NewWorkerController(coord, *runID, *agentID)
	if err := ctl.Start(os.Getpid()); err != nil {
		return err
	}

	spec, err := coord.ReadAgentSpec(*runID, *agentID)
	if err != nil {
		_ = ctl.Finish("", err)
		return err
	}

	maxTokensValue := *maxTokens
	if spec.MaxTokens > 0 {
		maxTokensValue = spec.MaxTokens
	}

	rt, err := newAgentRuntime(runtimeOptions{
		SkillsDir:      *skillsDir,
		Temperature:    *temperature,
		MaxTokens:      maxTokensValue,
		ConfigPath:     *configPath,
		MCPConfigPath:  *mcpConfigPath,
		MultiAgentRoot: *multiAgentRoot,
	})
	if err != nil {
		_ = ctl.Finish("", err)
		return err
	}
	defer func() {
		if closeErr := rt.Close(); closeErr != nil {
			fmt.Fprintln(os.Stderr, "warning:", closeErr)
		}
	}()

	ag, err := agent.New(rt.Client, rt.Registry, *skillsDir)
	if err != nil {
		_ = ctl.Finish("", err)
		return err
	}
	ag.SetPromptMode(agent.PromptModeWorker)
	ag.Temperature = float32(*temperature)
	if spec.Temperature != nil {
		ag.Temperature = float32(*spec.Temperature)
	}
	ag.MCPReload = rt.ReloadMCP

	result, runErr := ag.RunTask(context.Background(), spec.Task, agent.TaskOptions{
		MaxTurns: spec.MaxTurns,
		Hooks: agent.TaskHooks{
			BeforeModelCall: func(ctx context.Context) ([]llm.Message, error) {
				if err := ctl.Checkpoint(ctx, "before_model_call"); err != nil {
					return nil, err
				}
				cmds := ctl.DrainMessages()
				if len(cmds) == 0 {
					return nil, nil
				}
				msgs := make([]llm.Message, 0, len(cmds))
				for _, cmd := range cmds {
					role := "user"
					if v, ok := cmd.Payload["role"].(string); ok {
						switch strings.ToLower(strings.TrimSpace(v)) {
						case "system":
							role = "system"
						case "user":
							role = "user"
						}
					}

					text := ""
					if v, ok := cmd.Payload["text"].(string); ok {
						text = strings.TrimSpace(v)
					}
					if text == "" {
						if v, ok := cmd.Payload["content"].(string); ok {
							text = strings.TrimSpace(v)
						}
					}
					if text == "" {
						if v, ok := cmd.Payload["message"].(string); ok {
							text = strings.TrimSpace(v)
						}
					}
					if text == "" && len(cmd.Payload) > 0 {
						if raw, err := json.Marshal(cmd.Payload); err == nil {
							text = string(raw)
						}
					}

					if text == "" {
						continue
					}
					msgs = append(msgs, llm.Message{
						Role:    role,
						Content: fmt.Sprintf("[External message seq=%d] %s", cmd.Seq, text),
					})
				}
				return msgs, nil
			},
			BeforeToolCall: func(ctx context.Context, name string, arguments string) error {
				return ctl.BeforeTool(ctx, name, arguments)
			},
			AfterToolCall: func(ctx context.Context, name string, arguments string, result string, callErr error, duration time.Duration) error {
				return ctl.AfterTool(ctx, name, arguments, result, callErr, duration)
			},
		},
	})
	finishErr := ctl.Finish(result, runErr)
	if runErr != nil {
		return joinErrors(runErr, finishErr)
	}
	return finishErr
}

func newAgentRuntime(opts runtimeOptions) (*agentRuntime, error) {
	client, err := llm.NewClientFromConfig(opts.ConfigPath)
	if err != nil {
		return nil, err
	}
	if opts.MaxTokens > 0 {
		client.MaxTokens = opts.MaxTokens
	}

	registry := tools.NewRegistry()
	registerCoreTools(registry, opts.SkillsDir, opts.ConfigPath)

	cfgPath := strings.TrimSpace(opts.MCPConfigPath)
	if cfgPath == "" {
		cfgPath = "mcp.json"
	}
	mcpRuntime := mcpclient.NewRuntime(cfgPath)
	registeredMCPToolNames := make([]string, 0)

	reloadMCPInternal := func(ctx context.Context) (mcpclient.ReloadReport, error) {
		report, err := mcpRuntime.Reload(ctx)
		if err != nil {
			return report, err
		}
		registry.UnregisterMany(registeredMCPToolNames)
		for _, tool := range mcpRuntime.Tools() {
			registry.Register(tool)
		}
		registeredMCPToolNames = mcpRuntime.ToolNames()
		return report, nil
	}
	reloadMCP := func(ctx context.Context) (string, error) {
		report, err := reloadMCPInternal(ctx)
		if err != nil {
			return "", err
		}
		return report.String(), nil
	}

	registry.Register(&tools.MCPReloadTool{Reload: reloadMCP})
	if report, err := reloadMCPInternal(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	} else if len(report.Warnings) > 0 {
		fmt.Fprintln(os.Stderr, "warning:", report.String())
	}

	coord := multiagent.NewCoordinator(opts.MultiAgentRoot)
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	workDir, _ := os.Getwd()

	registry.Register(&tools.AgentRunCreateTool{Coordinator: coord})
	registry.Register(&tools.AgentRunListTool{Coordinator: coord})
	registry.Register(&tools.AgentSpawnTool{
		Coordinator:        coord,
		Executable:         executable,
		SkillsDir:          opts.SkillsDir,
		ConfigPath:         opts.ConfigPath,
		MCPConfigPath:      opts.MCPConfigPath,
		DefaultTemperature: opts.Temperature,
		DefaultMaxTokens:   opts.MaxTokens,
		WorkDir:            workDir,
	})
	registry.Register(&tools.AgentStateTool{Coordinator: coord})
	registry.Register(&tools.AgentProgressTool{Coordinator: coord})
	registry.Register(&tools.AgentWaitTool{Coordinator: coord})
	registry.Register(&tools.AgentControlTool{Coordinator: coord})
	registry.Register(&tools.AgentEventsTool{Coordinator: coord})
	registry.Register(&tools.AgentInspectTool{Coordinator: coord})
	registry.Register(&tools.AgentResultTool{Coordinator: coord})
	registry.Register(&tools.SubagentsTool{Coordinator: coord})
	registry.Register(&tools.AgentSignalSendTool{Coordinator: coord})
	registry.Register(&tools.AgentSignalWaitTool{Coordinator: coord})

	return &agentRuntime{
		Client:      client,
		Registry:    registry,
		Coordinator: coord,
		ReloadMCP:   reloadMCP,
		closeFn: func() error {
			return mcpRuntime.Close()
		},
	}, nil
}

func registerCoreTools(registry *tools.Registry, skillsDir string, configPath string) {
	registry.Register(&tools.ListFilesTool{})
	registry.Register(&tools.SearchTool{})
	registry.Register(&tools.ReadFileTool{})
	registry.Register(&tools.WriteFileTool{})
	registry.Register(&tools.EditFileTool{})
	registry.Register(&tools.MoveFileTool{})
	registry.Register(&tools.CopyFileTool{})
	registry.Register(&tools.DeleteFileTool{})
	registry.Register(&tools.ExecCommandTool{})
	registry.Register(&tools.TavilySearchTool{ConfigPath: configPath})
	registry.Register(&tools.TavilyExtractTool{ConfigPath: configPath})
	registry.Register(&tools.TavilyCrawlTool{ConfigPath: configPath})

	registry.Register(&tools.SkillListTool{SkillsDir: skillsDir})
	registry.Register(&tools.SkillLoadTool{SkillsDir: skillsDir})
	registry.Register(&tools.SkillCreateTool{SkillsDir: skillsDir})
	registry.Register(&tools.SkillInstallTool{SkillsDir: skillsDir})
}

func joinErrors(primary error, secondary error) error {
	if primary == nil {
		return secondary
	}
	if secondary == nil {
		return primary
	}
	return fmt.Errorf("%v; %v", primary, secondary)
}

func runSkills(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: skills <list|create|install>")
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("skills list", flag.ExitOnError)
		dir := fs.String("dir", defaultSkillsDir(), "skills directory")
		fs.Parse(args[1:])
		list, err := skills.LoadSkills(*dir)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("(no skills)")
			return nil
		}
		for _, s := range list {
			if s.Description != "" {
				fmt.Printf("%s - %s\n", s.Name, s.Description)
			} else {
				fmt.Println(s.Name)
			}
		}
		return nil
	case "create":
		fs := flag.NewFlagSet("skills create", flag.ExitOnError)
		dir := fs.String("dir", defaultSkillsDir(), "skills directory")
		name := fs.String("name", "", "skill name")
		desc := fs.String("description", "", "skill description")
		fs.Parse(args[1:])
		skill, err := skills.CreateSkill(*dir, *name, *desc)
		if err != nil {
			return err
		}
		fmt.Printf("created %s at %s\n", skill.Name, skill.Dir)
		return nil
	case "install":
		fs := flag.NewFlagSet("skills install", flag.ExitOnError)
		dir := fs.String("dir", defaultSkillsDir(), "skills directory")
		localPath := fs.String("local", "", "local skill directory to copy")
		repo := fs.String("repo", "", "github repo (owner/repo)")
		path := fs.String("path", "", "path within repo")
		ref := fs.String("ref", "main", "git ref")
		name := fs.String("name", "", "override skill folder name")
		fs.Parse(args[1:])

		ctx := context.Background()
		if *localPath != "" {
			skill, err := skills.InstallFromLocal(*dir, *localPath, *name)
			if err != nil {
				return err
			}
			fmt.Printf("installed %s at %s\n", skill.Name, skill.Dir)
			return nil
		}
		if *repo == "" {
			return fmt.Errorf("--repo is required when not using --local")
		}
		skill, err := skills.InstallFromGitHub(ctx, *dir, *repo, *path, *ref, *name)
		if err != nil {
			return err
		}
		fmt.Printf("installed %s at %s\n", skill.Name, skill.Dir)
		return nil
	default:
		return fmt.Errorf("unknown skills subcommand: %s", args[0])
	}
}

func defaultSkillsDir() string {
	if dir := strings.TrimSpace(os.Getenv("SKILLS_DIR")); dir != "" {
		return dir
	}
	return "skills"
}
