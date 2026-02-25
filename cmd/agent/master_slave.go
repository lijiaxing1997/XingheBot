package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"test_skill_agent/internal/agent"
	"test_skill_agent/internal/appinfo"
	"test_skill_agent/internal/cluster"
	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/multiagent"
	"test_skill_agent/internal/restart"
	"test_skill_agent/internal/slavelog"
	"test_skill_agent/internal/supervisor"
	"test_skill_agent/internal/tools"
)

func runMaster(args []string) error {
	fs := flag.NewFlagSet("master", flag.ExitOnError)
	fs.SetOutput(os.Stdout)
	fs.Usage = func() { printMasterUsage(fs.Output()) }
	skillsDir := fs.String("skills-dir", defaultSkillsDir(), "skills directory")
	temperature := fs.Float64("temperature", 0.2, "LLM temperature")
	maxTokens := fs.Int("max-tokens", 0, "max tokens for completion (overrides config)")
	chatToolMode := fs.String("chat-tool-mode", "dispatcher", "chat tool access: dispatcher (agent_* + remote_* + subagents only) or full")
	uiMode := fs.String("ui", "tui", "ui mode: tui (default) or plain")
	configPath := fs.String("config", "config.json", "path to config.json")
	mcpConfigPath := fs.String("mcp-config", "mcp.json", "path to MCP config")
	multiAgentRoot := fs.String("multi-agent-root", ".multi_agent/runs", "path to multi-agent run storage")
	listenAddr := fs.String("listen", "0.0.0.0:7788", "listen address for WS gateway (host:port)")
	wsPath := fs.String("ws-path", "/ws", "websocket path")
	redisURL := fs.String("redis-url", "", "redis url for presence/route (optional)")
	heartbeat := fs.Duration("heartbeat", 5*time.Second, "expected slave heartbeat interval")
	initFlag := fs.Bool("init", false, "initialize config/mcp/skills from embedded bundle, then exit")
	fs.Parse(args)

	resolvedSkillsDir := resolvePath(*skillsDir)
	if *initFlag {
		return runInit("master", *configPath, *mcpConfigPath, resolvedSkillsDir)
	}

	seen := map[string]bool{}
	fs.Visit(func(f *flag.Flag) {
		seen[strings.TrimSpace(f.Name)] = true
	})
	if params, ok, err := loadStartParams(*configPath); err != nil {
		return err
	} else if ok {
		if !seen["listen"] && params.Master.Listen != nil {
			if v := strings.TrimSpace(*params.Master.Listen); v != "" {
				*listenAddr = v
			}
		}
		if !seen["ws-path"] && params.Master.WSPath != nil {
			if v := strings.TrimSpace(*params.Master.WSPath); v != "" {
				*wsPath = v
			}
		}
		if !seen["ui"] && params.Master.UI != nil {
			if v := strings.TrimSpace(*params.Master.UI); v != "" {
				*uiMode = v
			}
		}
		if !seen["redis-url"] && params.Master.RedisURL != nil {
			if v := strings.TrimSpace(*params.Master.RedisURL); v != "" {
				*redisURL = v
			}
		}
		if !seen["heartbeat"] && params.Master.Heartbeat != nil {
			raw := strings.TrimSpace(*params.Master.Heartbeat)
			if raw != "" {
				d, err := time.ParseDuration(raw)
				if err != nil {
					return fmt.Errorf("start_params.master.heartbeat: %w", err)
				}
				*heartbeat = d
			}
		}
		if !seen["chat-tool-mode"] && params.Master.ChatToolMode != nil {
			if v := strings.TrimSpace(*params.Master.ChatToolMode); v != "" {
				*chatToolMode = v
			}
		}
	}
	controlPlaneOnly := true
	if strings.EqualFold(strings.TrimSpace(*chatToolMode), string(agent.ChatToolModeFull)) {
		controlPlaneOnly = false
	}

	secretStr, generated, err := cluster.EnsureClusterSecret(*configPath)
	if err != nil {
		return err
	}
	if generated {
		fmt.Fprintln(os.Stderr, "cluster.secret generated and saved to", strings.TrimSpace(*configPath))
	}
	secretBytes, err := cluster.DecodeSecretBase64(secretStr)
	if err != nil {
		return err
	}
	cCfg, err := cluster.LoadClusterConfig(*configPath)
	if err != nil {
		return err
	}

	var presence cluster.PresenceStore = cluster.NoopPresenceStore{}
	if strings.TrimSpace(*redisURL) != "" {
		store, err := cluster.NewRedisPresenceStore(*redisURL)
		if err != nil {
			return err
		}
		presence = store
		defer func() {
			if err := presence.Close(); err != nil {
				fmt.Fprintln(os.Stderr, "warning:", err)
			}
		}()
	}

	isTUI := func() bool {
		switch strings.ToLower(strings.TrimSpace(*uiMode)) {
		case "", "tui":
			return true
		default:
			return false
		}
	}()

	var gwLog *log.Logger
	var gwLogFile *os.File
	if isTUI {
		runRoot := resolvePath(*multiAgentRoot)
		logDir := filepath.Dir(runRoot)
		if strings.TrimSpace(logDir) == "" {
			logDir = ".multi_agent"
		}
		_ = os.MkdirAll(logDir, 0o755)
		f, err := os.OpenFile(filepath.Join(logDir, "master_gateway.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			gwLogFile = f
			gwLog = log.New(f, "", log.LstdFlags)
			defer func() { _ = gwLogFile.Close() }()
		}
	}

	gw, err := cluster.NewMasterGateway(cluster.MasterGatewayOptions{
		Secret:   secretBytes,
		Registry: cluster.NewSlaveRegistry(),
		Presence: presence,
		PresenceTTLSeconds: func() int {
			ttl := int((*heartbeat * 3) / time.Second)
			if ttl < 15 {
				ttl = 15
			}
			return ttl
		}(),
		Files:             cCfg.Files,
		HeartbeatInterval: *heartbeat,
		AcceptOriginAny:   true,
		Logf: func(format string, args ...any) {
			if gwLog != nil {
				gwLog.Printf(format, args...)
				return
			}
			if !isTUI {
				fmt.Fprintf(os.Stderr, format+"\n", args...)
			}
		},
	})
	if err != nil {
		return err
	}

	path := strings.TrimSpace(*wsPath)
	if path == "" {
		path = "/ws"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	mux := http.NewServeMux()
	mux.Handle(path, gw.WSHandler())
	srv := &http.Server{
		Addr:    strings.TrimSpace(*listenAddr),
		Handler: mux,
	}
	if strings.TrimSpace(srv.Addr) == "" {
		srv.Addr = "0.0.0.0:7788"
	}

	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	serverErr := make(chan error, 1)
	go func() {
		if cCfg.TLS.Enabled {
			cert := strings.TrimSpace(cCfg.TLS.CertFile)
			key := strings.TrimSpace(cCfg.TLS.KeyFile)
			if cert == "" || key == "" {
				serverErr <- errors.New("cluster.tls.enabled=true but cert_file/key_file are not set")
				return
			}
			serverErr <- srv.ServeTLS(ln, cert, key)
			return
		}
		serverErr <- srv.Serve(ln)
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

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
	defer func() {
		if err := rt.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "warning:", err)
		}
	}()

	rt.Registry.Register(&tools.RemoteSlaveListTool{Registry: gw.Registry()})
	rt.Registry.Register(&tools.RemoteSlaveDisconnectTool{Registry: gw.Registry(), Presence: presence})
	rt.Registry.Register(&tools.RemoteSlaveForgetTool{Registry: gw.Registry(), Presence: presence})
	rt.Registry.Register(&tools.RemoteAgentRunTool{Gateway: gw, Coordinator: rt.Coordinator})
	rt.Registry.Register(&tools.RemoteFilePutTool{Gateway: gw})
	rt.Registry.Register(&tools.RemoteFileGetTool{Gateway: gw})

	ag, err := agent.New(rt.Client, rt.Registry, resolvedSkillsDir)
	if err != nil {
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

	provider := clusterSlaveProvider{Registry: gw.Registry()}

	if rt.Restart != nil {
		if sentinel, _ := rt.Restart.ConsumeSentinel(); sentinel != nil {
			ag.StartupBanner = restart.FormatSentinelMessage(sentinel)
		}
	}

	var uiErr error
	switch strings.ToLower(strings.TrimSpace(*uiMode)) {
	case "", "tui":
		uiErr = ag.RunInteractiveTUI(context.Background(), os.Stdin, os.Stdout, agent.TUIOptions{
			Coordinator:   rt.Coordinator,
			ConfigPath:    *configPath,
			SlaveProvider: provider,
		})
	default:
		fmt.Printf("%s master ready. WS=%s%s (instance=%s)\n", appinfo.Display(), strings.TrimSpace(srv.Addr), path, gw.InstanceID())
		uiErr = ag.RunInteractive(context.Background(), os.Stdin, os.Stdout)
	}

	if uiErr != nil {
		return uiErr
	}

	if rt.Restart != nil && rt.Restart.IsRestartRequested() {
		// Best-effort cleanup before exec/exit (defers do not run on exec/os.Exit).
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
		_ = srv.Close()
		_ = ln.Close()
		if err := presence.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "warning:", err)
		}
		if gwLogFile != nil {
			_ = gwLogFile.Close()
		}
		if err := rt.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "warning:", err)
		}

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

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	default:
	}
	return nil
}

func runSlave(args []string) error {
	fs := flag.NewFlagSet("slave", flag.ExitOnError)
	fs.SetOutput(os.Stdout)
	fs.Usage = func() { printSlaveUsage(fs.Output()) }
	skillsDir := fs.String("skills-dir", defaultSkillsDir(), "skills directory")
	temperature := fs.Float64("temperature", 0.2, "LLM temperature")
	maxTokens := fs.Int("max-tokens", 0, "max tokens for completion (overrides config)")
	uiMode := fs.String("ui", "plain", "ui mode: plain (default) or tui")
	configPath := fs.String("config", "slave-config.json", "path to config.json")
	mcpConfigPath := fs.String("mcp-config", "mcp.json", "path to MCP config")
	multiAgentRoot := fs.String("multi-agent-root", ".multi_agent/runs", "path to multi-agent run storage")
	masterURL := fs.String("master", "", "master websocket url (ws://... or wss://...)")
	name := fs.String("name", "", "display name")
	slaveID := fs.String("id", "", "stable slave id (optional)")
	tags := fs.String("tags", "", "comma-separated tags (k=v,k=v)")
	heartbeat := fs.Duration("heartbeat", 5*time.Second, "heartbeat interval")
	maxInflight := fs.Int("max-inflight-runs", 1, "max concurrent agent.run executions")
	insecureSkipVerify := fs.Bool("insecure-skip-verify", false, "skip TLS certificate verification for wss:// (dangerous)")
	initFlag := fs.Bool("init", false, "initialize config/mcp/skills from embedded bundle, then exit")
	fs.Parse(args)

	resolvedSkillsDir := resolvePath(*skillsDir)
	if *initFlag {
		return runInit("slave", *configPath, *mcpConfigPath, resolvedSkillsDir)
	}

	seen := map[string]bool{}
	fs.Visit(func(f *flag.Flag) {
		seen[strings.TrimSpace(f.Name)] = true
	})
	if !seen["config"] && strings.TrimSpace(*configPath) == "slave-config.json" {
		if _, err := os.Stat("slave-config.json"); err != nil && os.IsNotExist(err) {
			if _, err := os.Stat("config.json"); err == nil {
				*configPath = "config.json"
			}
		}
	}
	if params, ok, err := loadStartParams(*configPath); err != nil {
		return err
	} else if ok {
		if !seen["master"] && params.Slave.MasterURL != nil {
			if v := strings.TrimSpace(*params.Slave.MasterURL); v != "" {
				*masterURL = v
			}
		}
		if !seen["name"] && params.Slave.Name != nil {
			if v := strings.TrimSpace(*params.Slave.Name); v != "" {
				*name = v
			}
		}
		if !seen["id"] && params.Slave.SlaveID != nil {
			if v := strings.TrimSpace(*params.Slave.SlaveID); v != "" {
				*slaveID = v
			}
		}
		if !seen["tags"] && params.Slave.Tags != nil {
			if v := strings.TrimSpace(*params.Slave.Tags); v != "" {
				*tags = v
			}
		}
		if !seen["heartbeat"] && params.Slave.Heartbeat != nil {
			raw := strings.TrimSpace(*params.Slave.Heartbeat)
			if raw != "" {
				d, err := time.ParseDuration(raw)
				if err != nil {
					return fmt.Errorf("start_params.slave.heartbeat: %w", err)
				}
				*heartbeat = d
			}
		}
		if !seen["max-inflight-runs"] && params.Slave.MaxInflightRuns != nil && *params.Slave.MaxInflightRuns > 0 {
			*maxInflight = *params.Slave.MaxInflightRuns
		}
		if !seen["insecure-skip-verify"] && params.Slave.InsecureSkipVerify != nil {
			*insecureSkipVerify = *params.Slave.InsecureSkipVerify
		}
	}

	if strings.TrimSpace(*masterURL) == "" {
		return errors.New("--master is required")
	}

	cCfg, err := cluster.LoadClusterConfig(*configPath)
	if err != nil {
		return err
	}
	secretBytes, err := cluster.DecodeSecretBase64(cCfg.Secret)
	if err != nil {
		return fmt.Errorf("cluster.secret missing/invalid in %s (copy it from the master config): %w", strings.TrimSpace(*configPath), err)
	}

	uiRaw := strings.ToLower(strings.TrimSpace(*uiMode))
	isTUI := false
	switch uiRaw {
	case "", "plain":
		// default
	case "tui":
		isTUI = true
	default:
		return fmt.Errorf("--ui must be tui or plain")
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
		ControlPlaneOnly: true,
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	var logger *slavelog.Logger
	closeRuntime := func() {
		cancel()
		if logger != nil {
			_ = logger.Close()
		}
		if err := rt.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "warning:", err)
		}
	}

	runRoot := resolvePath(*multiAgentRoot)
	logDir := filepath.Dir(runRoot)
	if strings.TrimSpace(logDir) == "" {
		logDir = ".multi_agent"
	}
	_ = os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, fmt.Sprintf("slave-%s.log", time.Now().Format("20060102-150405")))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		closeRuntime()
		return err
	}
	logger = slavelog.New(slavelog.Options{
		File:        logFile,
		Term:        os.Stderr,
		TermEnabled: !isTUI,
		TermColor:   slavelog.TermColorEnabled(os.Stderr),
	})

	monitor := slavelog.NewRunMonitor(rt.Coordinator, logger)
	go monitor.Run(ctx)

	autoCompactionPatch, autoCompactionOK, err := loadAutoCompactionPatchFromConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
		autoCompactionOK = false
	}

	runner := &headlessSlaveRunner{
		Coord: rt.Coordinator,
		Log:   logger,
		Mon:   monitor,
		NewAgent: func() (*agent.Agent, error) {
			a, err := agent.New(rt.Client, rt.Registry, resolvedSkillsDir)
			if err != nil {
				return nil, err
			}
			a.ConfigPath = *configPath
			a.SetPromptMode(agent.PromptModeChat)
			a.SetChatToolMode(agent.ChatToolModeDispatcher)
			a.Temperature = float32(*temperature)
			a.RestartManager = rt.Restart
			if autoCompactionOK {
				a.AutoCompaction = autoCompactionPatch.ApplyTo(a.AutoCompaction)
			}
			return a, nil
		},
	}
	if _, err := runner.NewAgent(); err != nil {
		closeRuntime()
		return err
	}

	meta := buildSlaveMeta(*tags)

	if strings.TrimSpace(*slaveID) == "" {
		idPath := defaultStableSlaveIDPath()
		id, generated, err := loadOrCreateStableSlaveID(idPath)
		if err != nil {
			if logger != nil {
				logger.Logf(slavelog.KindWarn, "stable slave id unavailable (path=%s): %v", idPath, err)
			}
		} else {
			*slaveID = id
			if generated && logger != nil {
				logger.Logf(slavelog.KindInfo, "stable slave id generated: %s (saved to %s)", strings.TrimSpace(id), idPath)
			}
		}
	}

	kindAndFormat := func(format string) (slavelog.Kind, string) {
		trimmed := strings.TrimSpace(format)
		lower := strings.ToLower(trimmed)
		switch {
		case strings.HasPrefix(lower, "cmd:"):
			return slavelog.KindCmd, strings.TrimSpace(trimmed[len("cmd:"):])
		case strings.HasPrefix(lower, "ws:"):
			return slavelog.KindWS, strings.TrimSpace(trimmed[len("ws:"):])
		case strings.HasPrefix(lower, "warn:"):
			return slavelog.KindWarn, strings.TrimSpace(trimmed[len("warn:"):])
		case strings.HasPrefix(lower, "error:"):
			return slavelog.KindError, strings.TrimSpace(trimmed[len("error:"):])
		default:
			return slavelog.KindWS, trimmed
		}
	}
	client, err := cluster.NewSlaveClient(cluster.SlaveClientOptions{
		MasterURL:          *masterURL,
		Secret:             secretBytes,
		SlaveID:            *slaveID,
		Name:               *name,
		Version:            appinfo.Display(),
		Capabilities:       []string{"remote_agent_run"},
		Meta:               meta,
		Files:              cCfg.Files,
		HeartbeatInterval:  *heartbeat,
		InsecureSkipVerify: cCfg.TLS.InsecureSkipVerify || *insecureSkipVerify,
		Runner:             runner,
		MaxInflightRuns:    *maxInflight,
		StopRequested: func() bool {
			return rt.Restart != nil && rt.Restart.IsRestartRequested()
		},
		Logf: func(format string, args ...any) {
			if logger == nil {
				fmt.Fprintf(os.Stderr, strings.TrimRight(format, "\n")+"\n", args...)
				return
			}
			kind, cleaned := kindAndFormat(format)
			logger.Logf(kind, cleaned, args...)
		},
	})
	if err != nil {
		closeRuntime()
		return err
	}

	if logger != nil {
		logger.Logf(
			slavelog.KindInfo,
			"%s slave starting. master=%s id=%s name=%s ui=%s heartbeat=%s max_inflight_runs=%d log=%s",
			appinfo.Display(),
			strings.TrimSpace(*masterURL),
			strings.TrimSpace(client.SlaveID()),
			strings.TrimSpace(*name),
			uiRaw,
			heartbeat.String(),
			*maxInflight,
			logPath,
		)
	}
	if isTUI {
		if logger != nil {
			logger.Logf(slavelog.KindInfo, "starting TUI (slave continues running in background)")
		}
		tuiAgent, err := runner.NewAgent()
		if err != nil {
			closeRuntime()
			return err
		}

		clientErrCh := make(chan error, 1)
		tuiErrCh := make(chan error, 1)

		go func() { clientErrCh <- client.Run(ctx) }()
		go func() {
			tuiErrCh <- tuiAgent.RunInteractiveTUI(ctx, os.Stdin, os.Stdout, agent.TUIOptions{
				Coordinator: rt.Coordinator,
				ConfigPath:  *configPath,
			})
		}()

		var (
			runErr error
			tuiErr error
		)
		select {
		case runErr = <-clientErrCh:
			cancel()
			select {
			case tuiErr = <-tuiErrCh:
			case <-time.After(2 * time.Second):
			}
		case tuiErr = <-tuiErrCh:
			cancel()
			select {
			case runErr = <-clientErrCh:
			case <-time.After(2 * time.Second):
				runErr = context.Canceled
			}
		}
		if tuiErr != nil && logger != nil {
			logger.Logf(slavelog.KindWarn, "tui exited: %v", tuiErr)
		}
		if errors.Is(runErr, context.Canceled) {
			runErr = nil
		}
		if errors.Is(runErr, cluster.ErrStopRequested) {
			runErr = nil
		}
		if rt.Restart != nil && rt.Restart.IsRestartRequested() {
			closeRuntime()

			if supervisor.IsSupervisedChild() {
				os.Exit(restart.ExitCodeRestartRequested)
			}

			if execErr := restart.ExecReplacement("", os.Args[1:]); execErr != nil {
				if _, spawnErr := restart.SpawnReplacement("", os.Args[1:]); spawnErr != nil {
					return joinErrors(execErr, spawnErr)
				}
				os.Exit(0)
			}
			return nil
		}

		closeRuntime()
		return runErr
	}

	runErr := client.Run(ctx)
	if errors.Is(runErr, cluster.ErrStopRequested) {
		runErr = nil
	}

	if rt.Restart != nil && rt.Restart.IsRestartRequested() {
		closeRuntime()

		if supervisor.IsSupervisedChild() {
			os.Exit(restart.ExitCodeRestartRequested)
		}

		if execErr := restart.ExecReplacement("", os.Args[1:]); execErr != nil {
			if _, spawnErr := restart.SpawnReplacement("", os.Args[1:]); spawnErr != nil {
				return joinErrors(execErr, spawnErr)
			}
			os.Exit(0)
		}
		return nil
	}

	closeRuntime()
	return runErr
}

type clusterSlaveProvider struct {
	Registry *cluster.SlaveRegistry
}

func (p clusterSlaveProvider) SnapshotSlaves() []agent.SlaveSummary {
	if p.Registry == nil {
		return nil
	}
	infos := p.Registry.Snapshot(false)
	out := make([]agent.SlaveSummary, 0, len(infos))
	for _, s := range infos {
		out = append(out, agent.SlaveSummary{
			SlaveID:  strings.TrimSpace(s.SlaveID),
			Name:     strings.TrimSpace(s.Name),
			Status:   string(s.Status),
			LastSeen: s.LastSeen,
		})
	}
	return out
}

type headlessSlaveRunner struct {
	Coord    *multiagent.Coordinator
	NewAgent func() (*agent.Agent, error)
	Log      *slavelog.Logger
	Mon      *slavelog.RunMonitor
}

const (
	headlessPrimaryAgentID      = "primary"
	headlessPrimaryHistoryFile  = "history.jsonl"
	headlessPrimaryAgentTask    = "primary chat session"
	headlessMaxRunTitleRunes    = 60
	headlessMaxHistoryLineBytes = 1024 * 1024
)

func oneLine(text string) string {
	s := strings.TrimSpace(text)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	return s
}

func remoteRunTitle(task string) string {
	s := oneLine(task)
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) > headlessMaxRunTitleRunes {
		s = string(r[:headlessMaxRunTitleRunes]) + "…"
	}
	return "remote: " + s
}

func appendJSONLLine(path string, payload any) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded := payload
	switch v := payload.(type) {
	case llm.Message:
		encoded = struct {
			llm.Message
			TS string `json:"ts,omitempty"`
		}{
			Message: v,
			TS:      time.Now().UTC().Format(time.RFC3339Nano),
		}
	case *llm.Message:
		if v != nil {
			encoded = struct {
				llm.Message
				TS string `json:"ts,omitempty"`
			}{
				Message: *v,
				TS:      time.Now().UTC().Format(time.RFC3339Nano),
			}
		}
	}
	data, err := json.Marshal(encoded)
	if err != nil {
		return err
	}
	if len(data) > headlessMaxHistoryLineBytes {
		return fmt.Errorf("jsonl payload too large: %d bytes", len(data))
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

func (r *headlessSlaveRunner) Run(ctx context.Context, task string, opts cluster.AgentRunOptions, metadata map[string]any) (string, string, error) {
	if r == nil || r.Coord == nil || r.NewAgent == nil {
		return "", "", errors.New("runner is not configured")
	}
	requestID := ""
	if metadata != nil {
		if v, ok := metadata["request_id"].(string); ok {
			requestID = strings.TrimSpace(v)
		}
	}
	runMeta := map[string]any{
		"source": "cluster_slave",
	}
	for k, v := range metadata {
		runMeta[k] = v
	}
	if title := remoteRunTitle(task); title != "" {
		runMeta["title"] = title
	}
	run, err := r.Coord.CreateRun("", runMeta)
	if err != nil {
		return "", "", err
	}
	if r.Mon != nil {
		r.Mon.AddRun(run.ID)
	}

	if _, _, createErr := r.Coord.CreateAgent(run.ID, multiagent.AgentSpec{
		ID:   headlessPrimaryAgentID,
		Task: headlessPrimaryAgentTask,
		Metadata: map[string]any{
			"type":   "primary",
			"source": "cluster_slave",
		},
	}); createErr != nil && !strings.Contains(strings.ToLower(createErr.Error()), "already exists") {
		// best-effort: keep running even if the agent dir cannot be created
		if r.Log != nil {
			r.Log.Logf(slavelog.KindWarn, "agent_run warn request_id=%s run_id=%s create_primary_agent=%v", requestID, run.ID, createErr)
		}
	}
	primaryAgentDir := r.Coord.AgentDir(run.ID, headlessPrimaryAgentID)
	historyPath := filepath.Join(primaryAgentDir, headlessPrimaryHistoryFile)

	if state, stErr := r.Coord.ReadAgentState(run.ID, headlessPrimaryAgentID); stErr == nil {
		now := time.Now().UTC()
		state.Status = multiagent.StatusRunning
		state.PID = os.Getpid()
		if state.StartedAt.IsZero() {
			state.StartedAt = now
		}
		state.UpdatedAt = now
		_ = r.Coord.UpdateAgentState(run.ID, state)
	}

	// Persist the task so the slave TUI can display remote runs.
	_ = appendJSONLLine(historyPath, llm.Message{Role: "user", Content: strings.TrimSpace(task)})

	ag, err := r.NewAgent()
	if err != nil {
		return "", "", err
	}
	if r.Log != nil {
		r.Log.Logf(
			slavelog.KindCmd,
			"agent_run begin request_id=%s run_id=%s max_turns=%d temperature=%v max_tokens=%d timeout_seconds=%d task=%s",
			requestID,
			run.ID,
			opts.MaxTurns,
			opts.Temperature,
			opts.MaxTokens,
			opts.TimeoutSeconds,
			slavelog.Preview(task, 240),
		)
	}
	toolNameByCallID := make(map[string]string)
	out, err := ag.RunHeadlessSessionWithHooks(ctx, run.ID, task, nil, agent.HeadlessSessionHooks{
		Emit: func(msg llm.Message) {
			_ = appendJSONLLine(historyPath, msg)
			if r.Log == nil {
				return
			}
			switch msg.Role {
			case "assistant":
				if len(msg.ToolCalls) == 0 {
					if strings.TrimSpace(msg.Content) != "" {
						r.Log.Logf(
							slavelog.KindInfo,
							"agent_run assistant request_id=%s run_id=%s msg=%s",
							requestID,
							run.ID,
							slavelog.Preview(msg.Content, 320),
						)
					}
					return
				}
				for _, call := range msg.ToolCalls {
					toolNameByCallID[call.ID] = call.Function.Name
					r.Log.Logf(
						slavelog.KindTool,
						"agent_run tool_start request_id=%s run_id=%s call_id=%s name=%s args=%s",
						requestID,
						run.ID,
						strings.TrimSpace(call.ID),
						strings.TrimSpace(call.Function.Name),
						slavelog.Preview(call.Function.Arguments, 320),
					)
				}
			case "tool":
				name := strings.TrimSpace(toolNameByCallID[msg.ToolCallID])
				kind := slavelog.KindResult
				if strings.HasPrefix(strings.TrimSpace(msg.Content), "ERROR:") {
					kind = slavelog.KindWarn
				}
				r.Log.Logf(
					kind,
					"agent_run tool_done request_id=%s run_id=%s call_id=%s name=%s result=%s",
					requestID,
					run.ID,
					strings.TrimSpace(msg.ToolCallID),
					name,
					slavelog.Preview(msg.Content, 320),
				)
			default:
				return
			}
		},
	})
	if r.Log != nil {
		if err != nil {
			r.Log.Logf(slavelog.KindWarn, "agent_run done request_id=%s run_id=%s error=%s", requestID, run.ID, slavelog.Preview(err.Error(), 320))
		} else {
			r.Log.Logf(slavelog.KindInfo, "agent_run done request_id=%s run_id=%s output=%s", requestID, run.ID, slavelog.Preview(out, 320))
		}
	}

	finished := time.Now().UTC()
	status := multiagent.StatusCompleted
	errText := ""
	if err != nil {
		status = multiagent.StatusFailed
		errText = err.Error()
	}
	if state, stErr := r.Coord.ReadAgentState(run.ID, headlessPrimaryAgentID); stErr == nil {
		state.Status = status
		state.Error = errText
		state.FinishedAt = finished
		state.UpdatedAt = finished
		_ = r.Coord.UpdateAgentState(run.ID, state)
	}
	_ = r.Coord.WriteResult(run.ID, headlessPrimaryAgentID, multiagent.AgentResult{
		RunID:      run.ID,
		AgentID:    headlessPrimaryAgentID,
		Status:     status,
		Output:     out,
		Error:      errText,
		FinishedAt: finished,
	})
	return out, run.ID, err
}

func buildSlaveMeta(tagsRaw string) map[string]any {
	meta := map[string]any{
		"os":   runtime.GOOS,
		"arch": runtime.GOARCH,
	}
	if host, _ := os.Hostname(); strings.TrimSpace(host) != "" {
		meta["hostname"] = strings.TrimSpace(host)
	}
	tags := parseTagMap(tagsRaw)
	if len(tags) > 0 {
		meta["tags"] = tags
	}
	return meta
}

func parseTagMap(raw string) map[string]string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil
	}
	parts := strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case ',', '，', ';', '；', '\n', '\t', ' ':
			return true
		default:
			return false
		}
	})
	out := make(map[string]string, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func defaultStableSlaveIDPath() string {
	base, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(base) == "" {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return "slave_id"
		}
		return filepath.Join(home, ".xinghebot", "slave_id")
	}
	return filepath.Join(base, appinfo.Name, "slave_id")
}

func loadOrCreateStableSlaveID(path string) (id string, generated bool, err error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return "", false, errors.New("stable slave id path is empty")
	}

	if data, err := os.ReadFile(p); err == nil {
		if v := strings.TrimSpace(string(data)); v != "" {
			return v, false, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}

	id = cluster.NewID("slave")
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return id, true, err
	}
	tmp := filepath.Join(dir, fmt.Sprintf(".slave_id.tmp.%d", time.Now().UTC().UnixNano()))
	if err := os.WriteFile(tmp, []byte(id+"\n"), 0o600); err != nil {
		_ = os.Remove(tmp)
		return id, true, err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return id, true, err
	}
	return id, true, nil
}
