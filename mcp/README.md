# MCP Directory Convention

This repository keeps all MCP server source code under `mcp/`.

## Required layout

- `mcp/<server-name>/`: MCP server source, tests, and server-local venv.
- `bin/<server-name>` or `bin/<server-name>-mcp`: executable wrapper used by `mcp.json`.

## Why

- Keeps project root clean.
- Makes MCP ownership and deployment boundaries explicit.
- Enables runtime reload (`mcp_reload` or `/mcp reload`) after MCP/config changes without restarting the Agent process.

## Example

- Server source: `mcp/calculator/`
- Wrapper: `bin/calculator-mcp`
- Config env path: `"PYTHONPATH": "${PYTHONPATH}:./mcp/calculator"`
