# MCP Integration and Runtime Reload Guide

## 1. Project structure (current convention)

- All MCP server source code must live under `mcp/<server-name>/`.
- Executable wrappers stay in `bin/` (for example `bin/calculator-mcp`).
- MCP server wiring should live in `mcp.json`; `config.json` stays focused on LLM settings.

Example `mcp.json`:

```json
{
  "mcp_servers": [
    {
      "name": "calculator",
      "transport": "command",
      "command": "./bin/calculator-mcp",
      "env": {
        "PYTHONPATH": "${PYTHONPATH}:./mcp/calculator"
      }
    }
  ]
}
```

## 2. No-restart MCP loading (already supported)

The agent now supports MCP runtime reload without restarting the agent process:

- Tool: `mcp_reload`
- Interactive command: `/mcp reload`

What it does:

1. Re-read `mcp.json` (or `--mcp-config` path).
2. Reconnect MCP sessions.
3. Restart MCP subprocesses when transport is `command`.
4. Replace MCP tools in the tool registry for subsequent turns.

This gives immediate MCP tool refresh while keeping the same Agent process alive.

## 3. Process-decoupled architecture (recommended next step)

To fully decouple Agent and MCP lifecycle, use a dedicated MCP manager process (`mcpd`):

1. Agent process:
- Handles user conversation and LLM calls.
- Calls `mcpd` control API for reload/status/restart.
- Consumes MCP tools from `mcpd` instead of managing child MCP sessions directly.

2. MCP manager process (`mcpd`):
- Owns all MCP server subprocesses and HTTP/SSE sessions.
- Maintains tool catalog and health.
- Supports hot reload of `mcp_servers` config.
- Can be restarted independently without restarting Agent.

3. Control/transport interface:
- Local control plane (Unix socket or localhost HTTP):
  - `POST /reload`
  - `GET /status`
  - `GET /tools`
  - `POST /restart/{server}`
- Data plane:
  - Agent connects to `mcpd` as a single MCP endpoint (streamable HTTP preferred).

Benefits:

- Agent uptime unaffected by MCP manager restart.
- Clear process boundaries and failure isolation.
- Easier supervised deployment (`launchd`, `systemd`, `supervisord`).

## 4. Migration path

1. Keep current runtime reload (`mcp_reload`) as baseline.
2. Introduce `cmd/mcpd` with same config schema.
3. Move connection/reload logic from Agent into `mcpd`.
4. Switch Agent MCP transport target from direct servers to `mcpd`.
5. Add health checks and auto-reconnect between Agent and `mcpd`.
