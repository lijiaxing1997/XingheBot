package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func binaryName() string {
	if len(os.Args) == 0 {
		return "agent"
	}
	name := strings.TrimSpace(filepath.Base(os.Args[0]))
	if name == "" {
		return "agent"
	}
	return name
}

func isHelpArg(arg string) bool {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "-h", "--help", "-help", "help":
		return true
	default:
		return false
	}
}

func printRootUsage(w io.Writer) {
	bin := binaryName()
	fmt.Fprintf(w, `%s - multi-agent CLI

Usage:
  %s [command] [options]

Commands:
  chat        Interactive chat (default)
  master      Start master (WS gateway + UI)
  slave       Start slave (connect to master)
  worker      Internal worker process
  skills      Manage local skills (list/create/install)

Config:
  - --config is optional; by default we look in the current directory.
  - Put default master/slave flags in config.json via:
      start_params.master / start_params.slave

Help:
  %s -h
  %s <command> -h
  %s help <command>
`, bin, bin, bin, bin, bin)
}

func printChatUsage(w io.Writer) {
	bin := binaryName()
	fmt.Fprintf(w, `Usage:
  %s chat [options]
  %s [options]        (same as "chat")

Options:
  --config <file>            Config file (default: ./config.json)
  --mcp-config <file>        MCP config (default: ./mcp.json)
  --skills-dir <dir>         Skills dir (default: ./skills or $SKILLS_DIR)
  --multi-agent-root <dir>   Multi-agent run storage (default: ./.multi_agent/runs)

  --ui <tui|plain>           UI mode (default: tui)
  --chat-tool-mode <mode>    Tool access: dispatcher|full (default: dispatcher)
  --temperature <float>      LLM temperature (default: 0.2)
  --max-tokens <n>           Override max tokens (default: 0 = provider default)

  --init                     Initialize config/mcp/skills from embedded bundle, then exit
`, bin, bin)
}

func printMasterUsage(w io.Writer) {
	bin := binaryName()
	fmt.Fprintf(w, `Usage:
  %s master [options]

Config defaults (config.json):
  start_params.master: listen, ws_path, ui, redis_url, heartbeat, chat_tool_mode

Options:
  --config <file>            Config file (default: ./config.json)
  --mcp-config <file>        MCP config (default: ./mcp.json)
  --skills-dir <dir>         Skills dir (default: ./skills or $SKILLS_DIR)
  --multi-agent-root <dir>   Multi-agent run storage (default: ./.multi_agent/runs)

  --listen <host:port>       WS gateway listen addr (default: 0.0.0.0:7788)
  --ws-path <path>           WebSocket path (default: /ws)
  --redis-url <url>          Redis url for presence/route (optional)
  --heartbeat <duration>     Expected slave heartbeat (default: 5s)

  --ui <tui|plain>           UI mode (default: tui)
  --chat-tool-mode <mode>    Tool access: dispatcher|full (default: dispatcher)
  --temperature <float>      LLM temperature (default: 0.2)
  --max-tokens <n>           Override max tokens (default: 0 = provider default)

  --init                     Initialize config/mcp/skills from embedded bundle, then exit
`, bin)
}

func printSlaveUsage(w io.Writer) {
	bin := binaryName()
	fmt.Fprintf(w, `Usage:
  %s slave [options]

Config defaults (slave-config.json / config.json):
  start_params.slave: master, id, name, tags, heartbeat, max_inflight_runs, insecure_skip_verify

Options:
  --config <file>            Config file (default: ./slave-config.json; falls back to ./config.json if present)
  --mcp-config <file>        MCP config (default: ./mcp.json)
  --skills-dir <dir>         Skills dir (default: ./skills or $SKILLS_DIR)
  --multi-agent-root <dir>   Multi-agent run storage (default: ./.multi_agent/runs)

  --ui <plain|tui>           UI mode (default: plain)
  --master <ws_url>          Master websocket url (required unless set in start_params.slave.master)
  --id <slave_id>            Stable slave id (optional)
  --name <name>              Display name (optional)
  --tags <k=v,k=v>           Comma-separated tags (optional)
  --heartbeat <duration>     Heartbeat interval (default: 5s)
  --max-inflight-runs <n>    Max concurrent agent.run (default: 1)
  --insecure-skip-verify     Skip TLS certificate verification for wss:// (dangerous)

  --temperature <float>      LLM temperature (default: 0.2)
  --max-tokens <n>           Override max tokens (default: 0 = provider default)

  --init                     Initialize config/mcp/skills from embedded bundle, then exit
`, bin)
}

func printWorkerUsage(w io.Writer) {
	bin := binaryName()
	fmt.Fprintf(w, `Usage:
  %s worker --run-id <id> --agent-id <id> [options]

Options:
  --config <file>            Config file (default: ./config.json)
  --mcp-config <file>        MCP config (default: ./mcp.json)
  --skills-dir <dir>         Skills dir (default: ./skills or $SKILLS_DIR)

  --run-root <dir>           Multi-agent run storage (default: ./.multi_agent/runs)
  --run-id <id>              Run id (required)
  --agent-id <id>            Agent id (required)

  --temperature <float>      Default worker temperature (default: 0.2)
  --max-tokens <n>           Default worker max tokens (default: 0)
`, bin)
}

func printSkillsUsage(w io.Writer) {
	bin := binaryName()
	fmt.Fprintf(w, `Usage:
  %s skills <list|create|install> [options]

Examples:
  %s skills list
  %s skills create --name my-skill --description "..."
  %s skills install --repo owner/repo --path skills/my-skill --ref main
`, bin, bin, bin, bin)
}
