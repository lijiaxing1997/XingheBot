---
name: ssh-deploy-slave
description: Deploy and manage cluster slaves over SSH (upload agent binary/config/skills/mcp and start/verify). Use when the user asks to deploy/start/update slaves on remote servers via ssh/scp.
metadata:
  short-description: SSH deploy & manage cluster slaves
---

# SSH Deploy Slave (主从管理)

目标：从 Master 机器用 SSH 把 `agent` 以 `slave` 模式部署到远端，并支持后续“更新二进制 / 更新配置 / 更新 skills / 更新 mcp.json / 重启与验证”等日常运维操作。

## 你需要先确认的关键点（必须问清楚）

1) **远端信息**：`host`、`port`、`user`、认证方式（SSH key 或密码）。
2) **Master WS 地址**：`ws://.../ws` 或 `wss://.../ws`。
3) **Slave 身份**：`slave_id`（稳定 ID）、`name`（展示名）、`tags`（可选）。
4) **要同步哪些文件**（可选择）：
   - 二进制：必选（Linux：`dist/agent-linux-amd64` / `dist/agent-linux-arm64`；Windows：`dist/agent-windows-amd64.exe` / `dist/agent-windows-arm64.exe`；或用户指定路径）
   - `slave-config.json`：可选（推荐给 Slave 独立 API Key）
   - `mcp.json`：可选
   - `skills/`：可选（推荐至少同步“自我进化”最小集合，见下）
5) **远端目录布局**：
   - Linux：例如 `/opt/agent`（推荐）或 `~/agent`
   - Windows：例如 `C:/opt/agent`（推荐用 `/` 斜杠），或直接用相对目录名 `agent`（默认落在远端用户的 `$HOME` 下，通常更省事）

> 安全提醒：不要默认把 Master 的 `config.json`（包含邮箱网关/各种密钥）原样发到远端。推荐单独准备 `slave-config.json`，只放 Slave 自己需要的 API Key / cluster.secret 等。

## 需要传输哪些文件（详细清单）

### 必传：二进制

远端最终需要一个可执行文件，例如：

- Linux：`dist/agent-linux-amd64` 或 `dist/agent-linux-arm64`
- Windows：`dist/agent-windows-amd64.exe` 或 `dist/agent-windows-arm64.exe`

建议部署到远端：

- Linux：`<remote_dir>/agent`
- Windows：`<remote_dir>/agent.exe`

### 可选：Slave 配置（`slave-config.json`）

Slave 节点必须有自己的 LLM 配置（至少包含 `api_key/base_url/model`），并且必须包含与 Master 相同的 `cluster.secret`。

推荐从 `config.exm.json` 复制出一个 **最小化** 的 `slave-config.json`，重点字段：

- `api_key`：Slave 自己的 key（不要复用 Master，除非你确认风险）
- `base_url/model/max_tokens`：按你的 provider 填
- `cluster.secret`：从 Master 的 `config.json` 里复制（Master 启动后会自动生成/写入）
- `cluster.tls.insecure_skip_verify`：若 Master 用自签 `wss://` 且你允许跳过校验，则设为 `true`
- `cluster.files`：可选（用于 WS 文件传输落盘目录与配额）

建议部署到远端：

- `<remote_dir>/slave-config.json`

### 可选：MCP 配置（`mcp.json`）

如果你希望 Slave 能使用 MCP 工具，需要把 `mcp.json` 同步到远端（以及确保远端具备对应 MCP server 的运行环境）。

建议部署到远端：

- `<remote_dir>/mcp.json`

### 可选（推荐）：Skills（让 Slave 具备“自我进化”能力）

默认只同步以下四个（推荐的最小集合）：

- `skills/skill-installer`
- `skills/skill-creator`
- `skills/mcp-builder`
- `skills/mcp-config-manager`

建议部署到远端：

- `<remote_dir>/skills/<skill-name>/...`

> 说明：同步 skills 后，Slave 侧 worker 子进程会在任务中按需加载这些 skills，从而具备“安装/创建 skill、生成/管理 mcp.json”等能力。

## 一键脚本

使用脚本：`skills/ssh-deploy-slave/scripts/deploy.sh`

脚本能力：
- 自动判断远端 **OS/架构**（Linux/Windows + amd64/arm64），自动选择/构建对应 `dist/agent-*`
- 默认同步：二进制 + `slave-config.json` + skills（默认最小集合）
- 可选同步：`mcp.json`、MCP 运行时文件（`bin/` + `mcp/`）、指定 skills（默认最小集合，可切换为全量）
- 可选启动/重启 Slave：
  - Linux：`nohup` + `slave.pid` + `slave.log`
  - Windows：PowerShell `Start-Process` + `slave.pid` + `slave.log`
- 支持 key 认证；密码全自动需要 `sshpass`（使用 `SSHPASS` 环境变量，不要把密码写进命令行）

### 示例：key 模式（推荐）

```bash
bash skills/ssh-deploy-slave/scripts/deploy.sh \
  --host 10.0.0.12 --user root --key ~/.ssh/id_ed25519 \
  --remote-dir /opt/agent \
  --config-src ./slave-config.json \
  --master ws://MASTER_IP:7788/ws \
  --id slave-01 --name build-01 --tags "os=linux,arch=amd64"
```

### 示例：Windows（OpenSSH Server）

> 建议 `--remote-dir agent`（落在远端 `$HOME\\agent`），或使用 `C:/opt/agent`（推荐用 `/` 斜杠）。

```bash
bash skills/ssh-deploy-slave/scripts/deploy.sh \
  --host 10.0.0.99 --user Administrator --key ~/.ssh/id_ed25519 \
  --remote-dir agent \
  --config-src ./slave-config.json \
  --master ws://MASTER_IP:7788/ws \
  --id win-01 --name win-slave-01 --tags "os=windows,arch=amd64"
```

### 示例：同步 config/mcp（推荐使用独立 slave-config.json）

```bash
bash skills/ssh-deploy-slave/scripts/deploy.sh \
  --host 10.0.0.12 --user root --key ~/.ssh/id_ed25519 \
  --remote-dir /opt/agent \
  --config-src ./slave-config.json \
  --sync-mcp --mcp-src ./mcp.json \
  --master ws://MASTER_IP:7788/ws \
  --id slave-01 --name build-01
```

### 示例：仅更新 skills（不重启）

```bash
bash skills/ssh-deploy-slave/scripts/deploy.sh \
  --host 10.0.0.12 --user root --key ~/.ssh/id_ed25519 \
  --remote-dir /opt/agent \
  --no-binary --no-sync-config --no-start
```

## 运维/管理操作（常用）

1) **往远端 Slave 增加 skill**
   - 本地准备好 `skills/<new-skill>/`
   - 用脚本 `--sync-skills --skills <new-skill>` 同步过去
   - 之后通过 Master 的 `remote_agent_run` 让 Slave 执行 `skill_list/skill_load/skill_install` 等验证与自我更新

2) **更新远端 `mcp.json`**
   - `--sync-mcp --mcp-src ./mcp.json`
   - 如果 `mcp.json` 引用了本地相对路径（例如 `./bin/calculator-mcp`、`./mcp/calculator`），还需要同步这些运行时文件：
     - `--sync-mcp-runtime`
     - 并在远端按 `mcp/calculator/requirements.txt` 初始化 Python venv（参考 `bin/calculator-mcp` 的提示）
   - Windows 说明：仓库自带的 `bin/calculator-mcp` 是 Bash wrapper，Windows 远端如果没有 Bash（Git-Bash/WSL）将无法直接运行；可选择：
     - 在 Windows 上安装 Git-Bash/WSL 后继续使用；或
     - 调整远端 `mcp.json`，把 `command` 改为 `python` 并直接执行 `mcp/calculator/calculator_mcp.py`（自行维护 venv/依赖）。
   - 新的 worker 子进程会自动读取新配置（需要的话也可以在任务里调用 `mcp_reload`）

3) **更新二进制并重启**
   - `--restart`（脚本会尝试 kill 老进程并拉起新进程）

4) **验证**
   - Linux 远端：`cat <remote_dir>/slave.pid && ps -p $(cat <remote_dir>/slave.pid) -o pid,cmd`
   - Windows 远端（PowerShell）：`Get-Content <remote_dir>\\slave.pid; Get-Process -Id (Get-Content <remote_dir>\\slave.pid)`
   - Master：TUI 左侧 Slaves 区出现在线节点；或调用 `remote_slave_list`
