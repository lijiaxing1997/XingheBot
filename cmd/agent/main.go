package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"test_skill_agent/internal/agent"
	"test_skill_agent/internal/appinfo"
	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/mcpclient"
	"test_skill_agent/internal/memory"
	"test_skill_agent/internal/multiagent"
	"test_skill_agent/internal/restart"
	"test_skill_agent/internal/skills"
	"test_skill_agent/internal/supervisor"
	"test_skill_agent/internal/tools"

	"golang.org/x/term"
)

type runtimeOptions struct {
	SkillsDir        string
	Temperature      float64
	MaxTokens        int
	ConfigPath       string
	MCPConfigPath    string
	MultiAgentRoot   string
	AutoCleanup      bool
	AllowRestart     bool
	ControlPlaneOnly bool
}

type agentRuntime struct {
	Client      *llm.Client
	Registry    *tools.Registry
	Coordinator *multiagent.Coordinator
	ReloadMCP   func(context.Context) (string, error)
	Restart     *restart.Manager
	closeFn     func() error
}

func (r *agentRuntime) Close() error {
	if r == nil || r.closeFn == nil {
		return nil
	}
	return r.closeFn()
}

func main() {
	args := os.Args[1:]
	if len(args) > 0 && strings.EqualFold(strings.TrimSpace(args[0]), "help") {
		if len(args) < 2 || isHelpArg(args[1]) {
			printRootUsage(os.Stdout)
			return
		}
		switch strings.ToLower(strings.TrimSpace(args[1])) {
		case "chat":
			printChatUsage(os.Stdout)
		case "master":
			printMasterUsage(os.Stdout)
		case "slave":
			printSlaveUsage(os.Stdout)
		case "worker":
			printWorkerUsage(os.Stdout)
		case "skills":
			printSkillsUsage(os.Stdout)
		default:
			printRootUsage(os.Stdout)
		}
		return
	}
	if len(args) > 0 && isHelpArg(args[0]) {
		printRootUsage(os.Stdout)
		return
	}
	if shouldAutoSuperviseChat(args) || shouldAutoSuperviseSlave(args) {
		code, err := supervisor.RunForegroundLoop(args)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		os.Exit(code)
	}

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
	case "master":
		if err := runMaster(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "slave":
		if err := runSlave(os.Args[2:]); err != nil {
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

func shouldAutoSuperviseChat(args []string) bool {
	if supervisor.IsSupervisedChild() || supervisor.SupervisorDisabled() {
		return false
	}
	if !isChatInvocation(args) {
		return false
	}
	if hasInitFlag(args) {
		return false
	}
	if hasHelpFlag(args) {
		return false
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false
	}
	return true
}

func shouldAutoSuperviseSlave(args []string) bool {
	if supervisor.IsSupervisedChild() || supervisor.SupervisorDisabled() {
		return false
	}
	if len(args) == 0 {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(args[0]), "slave") {
		return false
	}
	if hasInitFlag(args) {
		return false
	}
	if hasHelpFlag(args) {
		return false
	}
	return true
}

func isChatInvocation(args []string) bool {
	if len(args) == 0 {
		return true
	}
	switch args[0] {
	case "worker", "skills", "master", "slave":
		return false
	default:
		return true
	}
}

func hasInitFlag(args []string) bool {
	for _, a := range args {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if a == "--init" || a == "-init" || strings.HasPrefix(a, "--init=") || strings.HasPrefix(a, "-init=") {
			return true
		}
	}
	return false
}

func hasHelpFlag(args []string) bool {
	for _, a := range args {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		switch a {
		case "-h", "--help", "-help":
			return true
		}
		if strings.HasPrefix(a, "--help=") || strings.HasPrefix(a, "-help=") {
			return true
		}
	}
	return false
}

func runChat(args []string) error {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	fs.SetOutput(os.Stdout)
	fs.Usage = func() { printChatUsage(fs.Output()) }
	skillsDir := fs.String("skills-dir", defaultSkillsDir(), "skills directory")
	temperature := fs.Float64("temperature", 0.2, "LLM temperature")
	maxTokens := fs.Int("max-tokens", 0, "max tokens for completion (overrides config)")
	chatToolMode := fs.String("chat-tool-mode", "dispatcher", "chat tool access: dispatcher (agent_* + subagents only) or full")
	uiMode := fs.String("ui", "tui", "ui mode: tui (default) or plain")
	configPath := fs.String("config", "config.json", "path to config.json")
	mcpConfigPath := fs.String("mcp-config", "mcp.json", "path to MCP config")
	multiAgentRoot := fs.String("multi-agent-root", ".multi_agent/runs", "path to multi-agent run storage")
	initFlag := fs.Bool("init", false, "initialize config/mcp/skills from embedded bundle, then exit")
	fs.Parse(args)

	resolvedSkillsDir := resolvePath(*skillsDir)
	if *initFlag {
		return runInit("chat", *configPath, *mcpConfigPath, resolvedSkillsDir)
	}
	controlPlaneOnly := true
	if strings.EqualFold(strings.TrimSpace(*chatToolMode), string(agent.ChatToolModeFull)) {
		controlPlaneOnly = false
	}

	rt, err := newAgentRuntime(runtimeOptions{
		SkillsDir:        resolvedSkillsDir,
		Temperature:      *temperature,
		MaxTokens:        *maxTokens,
		ConfigPath:       *configPath,
		MCPConfigPath:    *mcpConfigPath,
		MultiAgentRoot:   *multiAgentRoot,
		AutoCleanup:      true,
		AllowRestart:     true,
		ControlPlaneOnly: controlPlaneOnly,
	})
	if err != nil {
		return err
	}
	closeRuntime := func() {
		if err := rt.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "warning:", err)
		}
	}

	ag, err := agent.New(rt.Client, rt.Registry, resolvedSkillsDir)
	if err != nil {
		closeRuntime()
		return err
	}
	ag.ConfigPath = *configPath
	ag.SetPromptMode(agent.PromptModeChat)
	ag.SetChatToolMode(agent.ChatToolMode(*chatToolMode))
	ag.Temperature = float32(*temperature)
	ag.MCPReload = rt.ReloadMCP
	ag.RestartManager = rt.Restart

	if replyStyle, err := loadReplyStyleFromConfig(*configPath); err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	} else if strings.TrimSpace(replyStyle) != "" {
		ag.SetReplyStyle(replyStyle)
	}
	if patch, ok, err := loadAutoCompactionPatchFromConfig(*configPath); err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	} else if ok {
		ag.AutoCompaction = patch.ApplyTo(ag.AutoCompaction)
	}

	if rt.Restart != nil {
		if sentinel, _ := rt.Restart.ConsumeSentinel(); sentinel != nil {
			ag.StartupBanner = restart.FormatSentinelMessage(sentinel)
		}
	}

	switch strings.ToLower(strings.TrimSpace(*uiMode)) {
	case "", "tui":
		err = ag.RunInteractiveTUI(context.Background(), os.Stdin, os.Stdout, agent.TUIOptions{
			Coordinator: rt.Coordinator,
			ConfigPath:  *configPath,
		})
	default:
		if controlPlaneOnly {
			fmt.Printf("%s ready. Type /restart to relaunch, or /exit to quit.\n", appinfo.Display())
		} else {
			fmt.Printf("%s ready. Type /mcp reload to refresh MCP servers, /restart to relaunch, or /exit to quit.\n", appinfo.Display())
		}
		err = ag.RunInteractive(context.Background(), os.Stdin, os.Stdout)
	}

	if err != nil {
		closeRuntime()
		return err
	}
	if rt.Restart != nil && rt.Restart.IsRestartRequested() {
		closeRuntime()

		// When supervised, signal the parent to respawn us.
		if supervisor.IsSupervisedChild() {
			os.Exit(restart.ExitCodeRestartRequested)
		}

		// When not supervised, prefer exec() so the restarted TUI stays in the
		// foreground job/process group.
		if execErr := restart.ExecReplacement("", os.Args[1:]); execErr != nil {
			if _, spawnErr := restart.SpawnReplacement("", os.Args[1:]); spawnErr != nil {
				return joinErrors(execErr, spawnErr)
			}
			os.Exit(0)
		}
		return nil
	}
	closeRuntime()
	return nil
}

func runWorker(args []string) error {
	fs := flag.NewFlagSet("worker", flag.ExitOnError)
	fs.SetOutput(os.Stdout)
	fs.Usage = func() { printWorkerUsage(fs.Output()) }
	skillsDir := fs.String("skills-dir", defaultSkillsDir(), "skills directory")
	temperature := fs.Float64("temperature", 0.2, "default worker temperature")
	maxTokens := fs.Int("max-tokens", 0, "default worker max tokens")
	configPath := fs.String("config", "config.json", "path to config.json")
	mcpConfigPath := fs.String("mcp-config", "mcp.json", "path to MCP config")
	multiAgentRoot := fs.String("run-root", ".multi_agent/runs", "path to multi-agent run storage")
	runID := fs.String("run-id", "", "run id")
	agentID := fs.String("agent-id", "", "agent id")
	fs.Parse(args)

	resolvedSkillsDir := resolvePath(*skillsDir)

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

	agentDir := coord.AgentDir(spec.RunID, spec.ID)
	assetDir := filepath.Join(agentDir, "asset")
	_ = os.MkdirAll(assetDir, 0o755)
	_ = os.Setenv("MULTI_AGENT_RUN_ROOT", coord.RunRoot)
	_ = os.Setenv("MULTI_AGENT_RUN_ID", spec.RunID)
	_ = os.Setenv("MULTI_AGENT_AGENT_ID", spec.ID)
	_ = os.Setenv("MULTI_AGENT_AGENT_DIR", agentDir)
	_ = os.Setenv("MULTI_AGENT_ASSET_DIR", assetDir)

	maxTokensValue := *maxTokens
	if spec.MaxTokens > 0 {
		maxTokensValue = spec.MaxTokens
	}

	rt, err := newAgentRuntime(runtimeOptions{
		SkillsDir:        resolvedSkillsDir,
		Temperature:      *temperature,
		MaxTokens:        maxTokensValue,
		ConfigPath:       *configPath,
		MCPConfigPath:    *mcpConfigPath,
		MultiAgentRoot:   *multiAgentRoot,
		AutoCleanup:      false,
		AllowRestart:     false,
		ControlPlaneOnly: false,
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

	ag, err := agent.New(rt.Client, rt.Registry, resolvedSkillsDir)
	if err != nil {
		_ = ctl.Finish("", err)
		return err
	}
	ag.ConfigPath = *configPath
	ag.SetPromptMode(agent.PromptModeWorker)
	ag.Temperature = float32(*temperature)
	if spec.Temperature != nil {
		ag.Temperature = float32(*spec.Temperature)
	}
	ag.MCPReload = rt.ReloadMCP
	if patch, ok, err := loadAutoCompactionPatchFromConfig(*configPath); err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	} else if ok {
		ag.AutoCompaction = patch.ApplyTo(ag.AutoCompaction)
	}

	maxTurns := spec.MaxTurns
	if workerCfg, err := multiagent.LoadWorkerConfig(*configPath); err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	} else if workerCfg.MaxTurns != nil && *workerCfg.MaxTurns > 0 {
		// Treat config as an upper bound to prevent runaway tool loops.
		if maxTurns <= 0 || maxTurns > *workerCfg.MaxTurns {
			maxTurns = *workerCfg.MaxTurns
		}
	}

	result, runErr := ag.RunTask(context.Background(), spec.Task, agent.TaskOptions{
		MaxTurns: maxTurns,
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
	var (
		reloadMCP  func(context.Context) (string, error)
		mcpRuntime *mcpclient.Runtime
	)

	registerMemoryTools(registry, opts.ConfigPath)
	if !opts.ControlPlaneOnly {
		registerCoreTools(registry, opts.SkillsDir, opts.ConfigPath)

		cfgPath := strings.TrimSpace(opts.MCPConfigPath)
		if cfgPath == "" {
			cfgPath = "mcp.json"
		}
		mcpRuntime = mcpclient.NewRuntime(cfgPath)
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
		reloadMCP = func(ctx context.Context) (string, error) {
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
	}

	coord := multiagent.NewCoordinator(opts.MultiAgentRoot)
	restartManager := restart.NewManager(restart.ResolveSentinelPath(opts.MultiAgentRoot))
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	cleanupDone := (<-chan struct{})(nil)
	if opts.AutoCleanup {
		if raw, err := multiagent.LoadAutoCleanupConfig(opts.ConfigPath); err != nil {
			fmt.Fprintln(os.Stderr, "warning:", err)
		} else if resolved, err := multiagent.ResolveAutoCleanupConfig(opts.MultiAgentRoot, raw); err != nil {
			fmt.Fprintln(os.Stderr, "warning:", err)
		} else {
			runner, err := multiagent.StartAutoCleanup(cleanupCtx, coord, resolved, nil)
			if err != nil {
				fmt.Fprintln(os.Stderr, "warning:", err)
			} else if runner != nil {
				cleanupDone = runner.Done
			}
		}
	}
	executable, err := os.Executable()
	if err != nil {
		cleanupCancel()
		return nil, err
	}
	workDir, _ := os.Getwd()

	// Background: daily rollup summary from sessions -> daily/*.md (skip child workers).
	if strings.TrimSpace(os.Getenv("MULTI_AGENT_AGENT_ID")) == "" {
		go memory.RunAutoDailySummaryLoop(cleanupCtx, client, opts.ConfigPath, workDir)
	}

	registry.Register(&tools.AgentRunCreateTool{Coordinator: coord})
	registry.Register(&tools.AgentRunListTool{Coordinator: coord})
	registry.Register(&tools.AgentRunPruneTool{Coordinator: coord})
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
	registry.Register(&tools.AgentControlTool{
		Coordinator:        coord,
		Executable:         executable,
		SkillsDir:          opts.SkillsDir,
		ConfigPath:         opts.ConfigPath,
		MCPConfigPath:      opts.MCPConfigPath,
		DefaultTemperature: opts.Temperature,
		DefaultMaxTokens:   opts.MaxTokens,
		WorkDir:            workDir,
	})
	registry.Register(&tools.AgentEventsTool{Coordinator: coord})
	registry.Register(&tools.AgentInspectTool{Coordinator: coord})
	registry.Register(&tools.AgentResultTool{Coordinator: coord})
	registry.Register(&tools.AgentSubagentHideTool{Coordinator: coord})
	registry.Register(&tools.AgentSubagentShowTool{Coordinator: coord})
	registry.Register(&tools.AgentSubagentListTool{Coordinator: coord})
	registry.Register(&tools.SubagentsTool{Coordinator: coord})
	registry.Register(&tools.AgentSignalSendTool{Coordinator: coord})
	registry.Register(&tools.AgentSignalWaitTool{Coordinator: coord})
	if opts.AllowRestart {
		registry.Register(&tools.AgentRestartTool{Manager: restartManager})
	}

	return &agentRuntime{
		Client:      client,
		Registry:    registry,
		Coordinator: coord,
		ReloadMCP:   reloadMCP,
		Restart:     restartManager,
		closeFn: func() error {
			cleanupCancel()
			if cleanupDone != nil {
				select {
				case <-cleanupDone:
				case <-time.After(2 * time.Second):
				}
			}
			if mcpRuntime != nil {
				return mcpRuntime.Close()
			}
			return nil
		},
	}, nil
}

func registerMemoryTools(registry *tools.Registry, configPath string) {
	if registry == nil {
		return
	}
	registry.Register(&tools.MemorySearchTool{ConfigPath: configPath})
	registry.Register(&tools.MemoryGetTool{ConfigPath: configPath})
	registry.Register(&tools.MemoryAppendTool{ConfigPath: configPath})
	registry.Register(&tools.MemoryFlushTool{ConfigPath: configPath})
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
	if len(args) == 0 || isHelpArg(args[0]) {
		printSkillsUsage(os.Stdout)
		return nil
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
	if cwd, err := os.Getwd(); err == nil {
		if root := findGitRoot(cwd); root != "" {
			return filepath.Join(root, "skills")
		}
	}
	return "skills"
}

func resolvePath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return filepath.Clean(trimmed)
	}
	return filepath.Clean(abs)
}

func loadReplyStyleFromConfig(configPath string) (string, error) {
	cfg, err := llm.LoadConfig(configPath)
	if err != nil {
		return "", err
	}

	replyStyle := cfg.Assistant.ReplyStyle
	enabled := false
	if replyStyle.Enabled != nil {
		enabled = *replyStyle.Enabled
	} else {
		enabled = strings.TrimSpace(replyStyle.Text) != "" || strings.TrimSpace(replyStyle.MDPath) != ""
	}
	if !enabled {
		return "", nil
	}

	if text := strings.TrimSpace(replyStyle.Text); text != "" {
		return text, nil
	}

	mdPath := strings.TrimSpace(replyStyle.MDPath)
	if mdPath == "" {
		return "", nil
	}

	configAbs := resolvePath(configPath)
	configDir := ""
	if configAbs != "" {
		configDir = filepath.Dir(configAbs)
	}
	if !filepath.IsAbs(mdPath) && configDir != "" {
		mdPath = filepath.Join(configDir, mdPath)
	}
	data, err := os.ReadFile(mdPath)
	if err != nil {
		return "", fmt.Errorf("load reply style md (%s): %w", mdPath, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func loadAutoCompactionPatchFromConfig(configPath string) (agent.AutoCompactionConfigPatch, bool, error) {
	cfg, err := llm.LoadConfig(configPath)
	if err != nil {
		return agent.AutoCompactionConfigPatch{}, false, err
	}
	raw := cfg.Assistant.AutoCompaction
	if len(bytes.TrimSpace(raw)) == 0 {
		return agent.AutoCompactionConfigPatch{}, false, nil
	}
	var patch agent.AutoCompactionConfigPatch
	if err := json.Unmarshal(raw, &patch); err != nil {
		return agent.AutoCompactionConfigPatch{}, false, fmt.Errorf("parse config.json.assistant.auto_compaction: %w", err)
	}
	return patch, true, nil
}

func findGitRoot(startDir string) string {
	current := filepath.Clean(strings.TrimSpace(startDir))
	if current == "" {
		return ""
	}
	for i := 0; i < 12; i++ {
		gitPath := filepath.Join(current, ".git")
		if info, err := os.Stat(gitPath); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return ""
}
