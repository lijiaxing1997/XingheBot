package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"test_skill_agent/internal/agent"
	"test_skill_agent/internal/appinfo"
	"test_skill_agent/internal/cluster"
	"test_skill_agent/internal/multiagent"
	"test_skill_agent/internal/tools"
)

func runMaster(args []string) error {
	fs := flag.NewFlagSet("master", flag.ExitOnError)
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
		return runInit(*configPath, *mcpConfigPath, resolvedSkillsDir)
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
			fmt.Fprintf(os.Stderr, format+"\n", args...)
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
	rt.Registry.Register(&tools.RemoteAgentRunTool{Gateway: gw})
	rt.Registry.Register(&tools.RemoteFilePutTool{Gateway: gw})
	rt.Registry.Register(&tools.RemoteFileGetTool{Gateway: gw})

	ag, err := agent.New(rt.Client, rt.Registry, resolvedSkillsDir)
	if err != nil {
		return err
	}
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

	switch strings.ToLower(strings.TrimSpace(*uiMode)) {
	case "", "tui":
		return ag.RunInteractiveTUI(context.Background(), os.Stdin, os.Stdout, agent.TUIOptions{
			Coordinator:   rt.Coordinator,
			ConfigPath:    *configPath,
			SlaveProvider: provider,
		})
	default:
		fmt.Printf("%s master ready. WS=%s%s (instance=%s)\n", appinfo.Display(), strings.TrimSpace(srv.Addr), path, gw.InstanceID())
		err := ag.RunInteractive(context.Background(), os.Stdin, os.Stdout)
		if err != nil {
			return err
		}
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
	skillsDir := fs.String("skills-dir", defaultSkillsDir(), "skills directory")
	temperature := fs.Float64("temperature", 0.2, "LLM temperature")
	maxTokens := fs.Int("max-tokens", 0, "max tokens for completion (overrides config)")
	configPath := fs.String("config", "config.json", "path to config.json")
	mcpConfigPath := fs.String("mcp-config", "mcp.json", "path to MCP config")
	multiAgentRoot := fs.String("multi-agent-root", ".multi_agent/runs", "path to multi-agent run storage")
	masterURL := fs.String("master", "", "master websocket url (ws://... or wss://...)")
	name := fs.String("name", "", "display name (for TUI)")
	slaveID := fs.String("id", "", "stable slave id (optional)")
	tags := fs.String("tags", "", "comma-separated tags (k=v,k=v)")
	heartbeat := fs.Duration("heartbeat", 5*time.Second, "heartbeat interval")
	maxInflight := fs.Int("max-inflight-runs", 1, "max concurrent agent.run executions")
	insecureSkipVerify := fs.Bool("insecure-skip-verify", false, "skip TLS certificate verification for wss:// (dangerous)")
	initFlag := fs.Bool("init", false, "initialize config/mcp/skills from embedded bundle, then exit")
	fs.Parse(args)

	resolvedSkillsDir := resolvePath(*skillsDir)
	if *initFlag {
		return runInit(*configPath, *mcpConfigPath, resolvedSkillsDir)
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

	rt, err := newAgentRuntime(runtimeOptions{
		SkillsDir:        resolvedSkillsDir,
		Temperature:      *temperature,
		MaxTokens:        *maxTokens,
		ConfigPath:       *configPath,
		MCPConfigPath:    *mcpConfigPath,
		MultiAgentRoot:   *multiAgentRoot,
		AutoCleanup:      true,
		AllowRestart:     false,
		ControlPlaneOnly: true,
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := rt.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "warning:", err)
		}
	}()

	ag, err := agent.New(rt.Client, rt.Registry, resolvedSkillsDir)
	if err != nil {
		return err
	}
	ag.SetPromptMode(agent.PromptModeChat)
	ag.SetChatToolMode(agent.ChatToolModeDispatcher)
	ag.Temperature = float32(*temperature)

	if patch, ok, err := loadAutoCompactionPatchFromConfig(*configPath); err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	} else if ok {
		ag.AutoCompaction = patch.ApplyTo(ag.AutoCompaction)
	}

	runner := &headlessSlaveRunner{
		Coord: rt.Coordinator,
		Agent: ag,
	}

	meta := buildSlaveMeta(*tags)
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
		Logf: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		},
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "%s slave starting. master=%s id=%s name=%s\n", appinfo.Display(), strings.TrimSpace(*masterURL), strings.TrimSpace(client.SlaveID()), strings.TrimSpace(*name))
	return client.Run(context.Background())
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
	Coord *multiagent.Coordinator
	Agent *agent.Agent
}

func (r *headlessSlaveRunner) Run(ctx context.Context, task string, opts cluster.AgentRunOptions, metadata map[string]any) (string, string, error) {
	if r == nil || r.Coord == nil || r.Agent == nil {
		return "", "", errors.New("runner is not configured")
	}
	runMeta := map[string]any{
		"source": "cluster_slave",
	}
	for k, v := range metadata {
		runMeta[k] = v
	}
	run, err := r.Coord.CreateRun("", runMeta)
	if err != nil {
		return "", "", err
	}
	out, err := r.Agent.RunHeadlessSession(ctx, run.ID, task, nil)
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
