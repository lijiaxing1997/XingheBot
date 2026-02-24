---
name: ssh-deploy-slave
description: Deploy and manage cluster slaves over SSH (upload xinghebot binary/config/skills/mcp and start/verify). Use when the user asks to deploy/start/update slaves on remote servers via ssh/scp.
metadata:
  short-description: SSH deploy & manage cluster slaves
---

# SSH Deploy Slave (主从管理)

目标：从 Master 机器用 SSH 把 `xinghebot` 以 `slave` 模式部署到远端，并支持后续“更新二进制 / 更新配置 / 更新 skills / 更新 mcp.json / 重启与验证”等日常运维操作。

> 新增：`xinghebot` 二进制支持 `--init`（master/slave/chat 均可用）。部署到远端后可先执行 `xinghebot slave --init` 自动生成配置模板（`slave-config.json`/`mcp.json`）并释放内置 skills（最小集合），从而避免手动同步 skills 目录。
>
> 新增：支持在配置里写默认启动参数 `start_params.master` / `start_params.slave`，从而把启动命令缩短为 `xinghebot master` / `xinghebot slave`（大部分参数无需再写在命令行）。

## 你需要先确认的关键点（必须问清楚）

1) **远端信息**：`host`、`port`、`user`、认证方式（SSH key 或密码）。
2) **Master WS 地址**：`ws://.../ws` 或 `wss://.../ws`。
3) **Slave 身份**：`slave_id`（稳定 ID）、`name`（展示名）、`tags`（可选）。
4) **远端目录布局**：
   - Linux：例如 `/opt/xinghebot`（推荐）或 `~/xinghebot`
   - Windows：例如 `C:/opt/xinghebot`（推荐用 `/` 斜杠），或直接用相对目录名 `xinghebot`（默认落在远端用户的 `$HOME` 下，通常更省事）

> 安全提醒：不要默认把 Master 的 `config.json`（包含邮箱网关/各种密钥）原样发到远端。推荐单独准备 `slave-config.json`，只放 Slave 自己需要的 API Key / cluster.secret 等。
>
> 同步策略（无需问）：现在默认走“上传二进制 -> 远端执行 `--init` ->（可选）覆盖上传 `slave-config.json`/`mcp.json` -> 启动/重启”。后续想新增/更新 skills，优先通过 Master 触发远端 `skill-installer` 安装/更新（无需再手动 scp `skills/`）。

## 需要传输哪些文件（详细清单）

### 必传：二进制

远端最终需要一个可执行文件，例如：

- Linux：`dist/xinghebot-linux-amd64` 或 `dist/xinghebot-linux-arm64`
- Windows：`dist/xinghebot-windows-amd64.exe` 或 `dist/xinghebot-windows-arm64.exe`

建议部署到远端：

- Linux：`<remote_dir>/xinghebot`
- Windows：`<remote_dir>/xinghebot.exe`

> 跨平台提示：如果你要把 Slave 部署到另一种 OS/架构（例如从 macOS 部署到 Linux/Windows），请确保本机 `dist/` 目录下已有对应的二进制文件；脚本会检测缺失并提示你下载/构建。

### 可选：Slave 配置（`slave-config.json`）

Slave 节点必须有自己的 LLM 配置（至少包含 `model_config.api_key/base_url/model`），并且必须包含与 Master 相同的 `cluster.secret`。

推荐直接使用 `xinghebot slave --init` 生成的 **最小化** `slave-config.json` 模板（或从 `slave-config.exm.json` 复制），重点字段：

- `model_config.api_key`：Slave 自己的 key（不要复用 Master，除非你确认风险）
- `model_config.base_url/model/max_tokens`：按你的 provider 填
- `cluster.secret`：从 Master 的 `config.json` 里复制（Master 启动后会自动生成/写入）
- `cluster.tls.insecure_skip_verify`：若 Master 用自签 `wss://` 且你允许跳过校验，则设为 `true`
- `cluster.files`：可选（用于 WS 文件传输落盘目录与配额）

另外建议把 Slave 的默认启动参数写进配置，避免每次命令行写一长串（可按需裁剪）：

```json
{
  "start_params": {
    "slave": {
      "master": "ws://MASTER_IP:7788/ws",
      "heartbeat": "5s",
      "max_inflight_runs": 1,
      "insecure_skip_verify": false
    }
  }
}
```

这样远端启动可简化为：

- `xinghebot slave --config slave-config.json --id slave-01 --name build-01`
- 甚至在 `start_params.slave` 里也配置 `id/name` 后：`xinghebot slave --config slave-config.json`

建议部署到远端：

- `<remote_dir>/slave-config.json`

### 可选：MCP 配置（`mcp.json`）

如果你希望 Slave 能使用 MCP 工具，需要把 `mcp.json` 同步到远端（以及确保远端具备对应 MCP server 的运行环境）。

建议部署到远端：

- `<remote_dir>/mcp.json`

### 可选（推荐）：Skills（让 Slave 具备“自我进化”能力）

一般不需要手动同步 skills：远端执行过 `--init` 后已释放以下六个（推荐的最小集合）：

- `skills/skill-installer`
- `skills/skill-creator`
- `skills/mcp-builder`
- `skills/mcp-config-manager`
- `skills/ssh-deploy-slave`
- `skills/slave-file-manager`

建议部署到远端：

- `<remote_dir>/skills/<skill-name>/...`

> 说明：同步 skills 后，Slave 侧 worker 子进程会在任务中按需加载这些 skills，从而具备“安装/创建 skill、生成/管理 mcp.json”等能力。

## 一键脚本

使用脚本：`skills/ssh-deploy-slave/scripts/deploy.sh`

脚本能力：
- 自动判断远端 **OS/架构**（Linux/Windows + amd64/arm64），自动选择/构建对应 `dist/xinghebot-*`
- 默认：上传二进制后执行远端 `xinghebot slave --init`（生成 `slave-config.json`/`mcp.json` 模板，并释放内置 skills 最小集合）
- 默认同步：二进制 + `slave-config.json`（用于写入真实 `model_config.api_key` / `cluster.secret` 等；skills 同步默认关闭）
- 可选同步：`mcp.json`、MCP 运行时文件（`bin/` + `mcp/`）、指定 skills（默认最小集合，可切换为全量）
- 可选启动/重启 Slave：
  - Linux：`nohup` + `slave.pid` + `slave.log`
  - Windows：PowerShell `Start-Process` + `slave.pid` + `slave.log`
- 支持 key 认证；密码全自动需要 `sshpass`（推荐 `SSHPASS` 环境变量或脚本的 `--password-file`；不要把密码写进命令行）

### 示例：key 模式（推荐）

```bash
bash skills/ssh-deploy-slave/scripts/deploy.sh \
  --host 10.0.0.12 --user root --key ~/.ssh/id_ed25519 \
  --remote-dir /opt/xinghebot \
  --config-src ./slave-config.json \
  --id slave-01 --name build-01 --tags "os=linux,arch=amd64"
```

> 如果你的 `slave-config.json` **没有** 配 `start_params.slave.master`，请在脚本里显式传 `--master ws://.../ws`；否则可省略（脚本会从配置读取/校验）。

### 示例：Windows（OpenSSH Server）

> 建议 `--remote-dir xinghebot`（落在远端 `$HOME\\xinghebot`），或使用 `C:/opt/xinghebot`（推荐用 `/` 斜杠）。

```bash
bash skills/ssh-deploy-slave/scripts/deploy.sh \
  --host 10.0.0.99 --user Administrator --key ~/.ssh/id_ed25519 \
  --remote-dir xinghebot \
  --config-src ./slave-config.json \
  --id win-01 --name win-slave-01 --tags "os=windows,arch=amd64"
```

### 示例：同步 config/mcp（推荐使用独立 slave-config.json）

```bash
bash skills/ssh-deploy-slave/scripts/deploy.sh \
  --host 10.0.0.12 --user root --key ~/.ssh/id_ed25519 \
  --remote-dir /opt/xinghebot \
  --config-src ./slave-config.json \
  --sync-mcp --mcp-src ./mcp.json \
  --id slave-01 --name build-01
```

### 示例：仅更新 skills（不重启）

```bash
bash skills/ssh-deploy-slave/scripts/deploy.sh \
  --host 10.0.0.12 --user root --key ~/.ssh/id_ed25519 \
  --remote-dir /opt/xinghebot \
  --sync-skills --no-remote-init \
  --no-binary --no-sync-config --no-start
```

## 运维/管理操作（常用）

1) **往远端 Slave 增加/更新 skill（推荐不 scp）**
   - 确保远端已执行过 `--init`（已包含 `skill-installer` 等最小集合）
   - 之后通过 Master 的 `remote_agent_run` 让 Slave 执行 `skill_install`（从仓库安装/更新），即可完成扩展
   - 仅当离线/本地未提交 skill 时，才用脚本 `--sync-skills --skills <new-skill>` 物理同步过去

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
