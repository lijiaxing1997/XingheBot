---
name: xinghebot-dev-manual
description: XingheBot 项目架构/功能/配置与二次开发手册（含 config.exm.json 逐项解释 + config.json 修改/重启生效指引）。当用户询问“项目怎么实现/配置怎么填/源码在哪里/如何自我迭代”时加载。
---

# XingheBot 开发手册（给 Agent 自己用）

> 目标：让 Agent 在运行时能“读懂自己”——知道项目架构、关键功能与代码位置；并在用户希望二次开发/自我迭代时，像开发手册一样给出可执行的指引。

## 使用规则（重要）

1. **当用户问“架构/模块/源码位置/配置字段含义/怎么改 config.json 生效/怎么二次开发”**：优先引用本 skill 的对应章节回答，并给出**具体文件路径**（用反引号包裹）。
2. **不要把真实密钥写进回复或 commit**：任何 `api_key/authorization_code/cluster.secret` 都属于敏感信息，只给出“应该放在哪里/怎么填”。
3. **用户修改 `config.json` 后如何生效**：默认建议用 `/restart` 或 `agent_restart` 触发自重启，让配置一次性生效（见下文“修改 config.json 并让它生效”）。

---

## 1) 项目目录结构速览（从“找代码”角度）

- `cmd/agent/`：CLI 入口（`xinghebot chat|master|slave|worker|skills`）与启动参数解析。
  - `cmd/agent/main.go`：主入口 + `newAgentRuntime`（把 LLM / tools / multi-agent / cron&heartbeat / restart 串起来）。
  - `cmd/agent/master_slave.go`：分布式 Master/Slave（WebSocket）。
  - `cmd/agent/start_params.go`：从 `config.json.start_params` 读取默认 CLI 参数（master/slave）。
- `internal/agent/`：核心 Agent（system prompt、tool policy、TUI、自动压缩 auto_compaction、技能热加载等）。
- `internal/tools/`：所有“工具函数”(tool-calling)实现：文件/exec/搜索/skills/MCP/cron/heartbeat/memory/multi-agent/远程 slave 等。
- `internal/llm/`：LLM provider 适配（OpenAI-compatible & Anthropics），读取 `config.json.model_config`。
- `internal/multiagent/`：多进程子 Agent 协作（run 目录结构、状态/事件/结果文件协议、自动清理）。
- `internal/cluster/`：Master/Slave 集群（鉴权、presence、远程 agent_run、远程文件传输）。
- `internal/mcpclient/`：MCP 客户端运行时（读取 `mcp.json`，连接 server，暴露 MCP tools）。
- `internal/memory/`：长期记忆（`MEMORY.md`/daily/sessions/…）、检索、脱敏、每日总结。
- `internal/autonomy/`：Autonomy 配置（cron + heartbeat）。
  - `internal/autonomy/cronrunner/`：cron 调度器（可选 email gateway 回传）。
  - `internal/autonomy/heartbeatrunner/`：heartbeat 定时器（基于 `HEARTBEAT.md`）。
- `internal/gateway/`：Email Gateway（IMAP 拉取、SMTP 发送）。
- `internal/bootstrap/`：把 `config.exm.json` / `mcp.exm.json` / 内置 skills 打包进二进制，用于 `--init` 初始化释放。
- `docs/`：设计文档与使用说明（建议优先看这些再读源码）：
  - `docs/构建说明.md`、`docs/MULTI_AGENT_GUIDE.md`、`docs/SKILLS_GUIDE.md`、`docs/MCP_INTEGRATION_GUIDE.md` 等。

---

## 2) 运行形态与入口（用户可见的“启动方式”）

- 交互单机模式：`xinghebot chat`（默认 TUI）
  - 代码：`cmd/agent/main.go` → `runChat`
- 分布式 Master：`xinghebot master`（TUI + WS 网关）
  - 代码：`cmd/agent/master_slave.go` → `runMaster`
- 分布式 Slave：`xinghebot slave`（默认 plain；可 `--ui=tui`）
  - 代码：`cmd/agent/master_slave.go` → `runSlave`
- 子 Agent worker 进程：`xinghebot worker`
  - 代码：`cmd/agent/main.go` → `runWorker`（通常由 `agent_spawn` 自动拉起）
- skills 管理命令：`xinghebot skills list|create|install`
  - 代码：`cmd/agent/main.go` → `runSkills`

提示：`chat/master/slave` 在合适条件下会自动进入 supervisor 前台循环（`internal/supervisor/`），从而支持“自重启”（见后文）。

---

## 3) 核心运行时拼装（理解“代码怎么串起来”）

### 3.1 newAgentRuntime：一次性把系统装配好

入口：`cmd/agent/main.go` 的 `newAgentRuntime(opts)`

做的事情（按顺序）：

1. **创建 LLM Client**：`internal/llm.NewClientFromConfig(opts.ConfigPath)`（读取 `config.json.model_config`）。
2. **创建 Tool Registry**：`internal/tools.NewRegistry()`。
3. **注册 memory 工具（即使是 dispatcher 也有）**：`registerMemoryTools()` → `memory_search/memory_get/memory_append/memory_flush`。
4. 若 `ControlPlaneOnly=false`（`chat --chat-tool-mode full` 或 worker）：
   - 注册“核心执行工具”：文件/搜索/exec/tavily/skill_*（`registerCoreTools`）。
   - 创建 MCP runtime：`internal/mcpclient.NewRuntime(mcp.json)`，并注册 `mcp_reload` + MCP tools（`<server>__<tool>`）。
5. **Multi-agent**：
   - 协调器：`internal/multiagent.NewCoordinator(opts.MultiAgentRoot)`（默认 `.multi_agent/runs`）。
   - `agent_*` / `subagents` 等工具注册在 `newAgentRuntime` 尾部。
6. **Restart**：
   - `internal/restart.NewManager(...)` + tool `agent_restart`（允许自重启）。
7. **后台循环（仅主进程，不在子 worker 内跑）**：
   - memory 每日总结：`internal/memory.RunAutoDailySummaryLoop(...)`
   - cron runner：`internal/autonomy/cronrunner.Start(...)`
   - heartbeat runner：`internal/autonomy/heartbeatrunner.Start(...)`
8. **自动清理 runs**：读取 `config.json.multi_agent.cleanup`，启动 `internal/multiagent.StartAutoCleanup(...)`。

### 3.2 Agent：system prompt + 工具策略 + UI

核心文件：`internal/agent/agent.go`、`internal/agent/tui.go`

关键点：

- `Agent.buildSystemPrompt()` 会把 `<available_skills> ... </available_skills>` 注入 system prompt，供模型按需加载。
- dispatcher 模式下，主 Agent 倾向“只调度不执行”（避免主 agent 直接动文件/exec）；worker 拥有完整工具权限。
- `assistant.auto_compaction` 配置会影响 overflow 后“总结+重试”策略：`internal/agent/auto_compaction.go`。

---

## 4) 功能模块 → 代码位置（按“我想改什么”索引）

### LLM / 模型接入

- 配置解析：`internal/llm/openai.go`（`LoadConfig`, `NewClientFromConfig`）
- provider 类型：`internal/llm/model_type.go`（支持 `openai`/`anthropics`）,注意在config.json里是 `model_type` 字段
- Anthropics 适配：`internal/llm/anthropic.go`

### 工具系统（tool-calling）

- 工具注册：`cmd/agent/main.go`（`registerMemoryTools` / `registerCoreTools` / 注册 `agent_*`/`cron_*`/`heartbeat_*`）
- 工具实现：`internal/tools/*.go`
  - 文件类：`internal/tools/files.go`
  - exec：`internal/tools/exec.go`
  - 本地搜索：`internal/tools/search.go`
  - Skills：`internal/tools/skills.go`
  - Tavily Web Search：`internal/tools/tavily.go`
  - MCP：`internal/tools/mcp.go`
  - Cron：`internal/tools/cron*.go`
  - Heartbeat：`internal/tools/heartbeat.go`
  - Restart：`internal/tools/restart.go`

### Skills（本地工作流/说明文档）

- skill 发现与解析：`internal/skills/skills.go`（frontmatter + 目录约定 `skills/<name>/SKILL.md`）
- GitHub 安装：`internal/skills/github.go`（供 `skill_install` 与 CLI `skills install` 使用）
- 使用说明：`docs/SKILLS_GUIDE.md`

### Multi-Agent（多进程子 Agent）

- run 存储与协议：`internal/multiagent/*`
- 核心工具：`internal/tools/multiagent*.go`（`agent_spawn/agent_wait/agent_result/...`）
- 使用说明：`docs/MULTI_AGENT_GUIDE.md`

### 分布式 Master/Slave（WebSocket）

- CLI 与网关：`cmd/agent/master_slave.go`
- 协议/鉴权/文件传输：`internal/cluster/*`
- Master 侧远程工具：`internal/tools/remote_cluster.go`、`internal/tools/remote_files.go`
- 设计说明：`docs/DISTRIBUTED_MASTER_SLAVE_DESIGN.md`

### MCP（Model Context Protocol）

- 配置解析：`internal/mcpclient/config.go`（读取 `mcp.json`）
- 连接与运行时：`internal/mcpclient/connect.go`、`internal/mcpclient/runtime.go`
- tool 封装：`internal/mcpclient/tool.go`（暴露为 `<server>__<tool>`）
- 使用说明：`docs/MCP_INTEGRATION_GUIDE.md`

### Autonomy：Cron / Heartbeat

- 配置：`internal/autonomy/config.go`（读取 `config.json.autonomy`）
- cron runner：`internal/autonomy/cronrunner/*` + tools `internal/tools/cron*.go`
- heartbeat runner：`internal/autonomy/heartbeatrunner/*` + tools `internal/tools/heartbeat.go`
- 设计说明：`docs/HEARTBEAT_CRON_DESIGN.md`、`docs/AUTONOMY_GUIDE.md`

### 长期记忆（Long-term Memory）

- 配置：`internal/memory/config.go`（读取 `config.json.memory`）
- 路径派生：`internal/memory/paths.go`（`workspace_dir/project_key/root_dir` 逻辑）
- 检索/读写：`internal/memory/fs.go`、`internal/memory/flush.go`
- tools：`internal/tools/memory.go`
- 设计说明：`docs/LONG_TERM_MEMORY_DESIGN.md`

### 自重启（restart）与 supervisor

- restart manager：`internal/restart/manager.go`，tool：`internal/tools/restart.go`（`agent_restart`）
- supervisor：`internal/supervisor/supervisor.go`（前台循环，收到“重启请求”后重拉起子进程）

### Email Gateway

- 配置：`internal/gateway/config.go`（读取 `config.json.gateway`）
- 实现：`internal/gateway/email_gateway.go`
- cron runner 会在满足配置时用 gateway 发邮件：`internal/autonomy/cronrunner/runner.go`

### 初始化资源打包（`--init`）

- 初始化释放逻辑：`internal/bootstrap/init.go`
- bundle 生成器：`internal/bootstrap/cmd/gen/main.go`
- 构建脚本：`scripts/build_dist.sh`（会先 `go generate ./internal/bootstrap`）

---

## 5) 配置文件总览：`config.exm.json` 与 `config.json`

- `config.exm.json`：**示例/模板**。`xinghebot ... --init` 会把它释放为 `config.json`（并同时释放 `mcp.json` 与部分内置 skills）。
- `config.json`：**真实运行配置**。程序启动会读取它（不同模块读取频率不同；最稳妥是改完重启）。
- `mcp.exm.json` / `mcp.json`：MCP servers 配置（可在运行时用 `mcp_reload` 生效，无需重启）。

---

## 6) `config.exm.json` 字段逐条解释（按文件顺序）

> 说明：这里解释的是 **模板字段**；真实运行请看 `config.json`。括号里给出主要读取代码位置（方便你跳转）。

### 6.1 `model_config`（LLM 供应商/模型）

读取位置：`internal/llm/openai.go`（`LoadConfig`, `NewClientFromConfig`）

- `model_config.model_type`：模型供应商类型。
  - 支持：`openai`（默认）/ `anthropic`（兼容 `anthropics` 拼写）。
  - 代码：`internal/llm/model_type.go`
- `model_config.api_key`：API Key（必填，除非你走环境变量/自定义逻辑；本项目默认要求这里有值）。
- `model_config.base_url`：Base URL（可为空）。
  - `openai` 兼容默认：`https://api.openai.com`
  - `anthropicc` 兼容默认：见 `internal/llm/anthropic.go` 的默认常量
- `model_config.model`：模型名。
  - `openai` 兼容：为空时默认 `gpt-4o-mini`（见 `internal/llm/openai.go`）
  - `anthropicc`：必须显式填写（否则报错）
- `model_config.max_tokens`：单次 completion 上限。
  - **OpenAI-compatible**：当为 `0` 时，会省略 `max_tokens` 参数（让 provider 用默认值）。
  - **Anthropics**：需要 `max_tokens`；若为 `0`，程序会用一个默认值（当前注释写的是 1024，见模板 `notes`）。

### 6.2 `web_search`（Tavily Web Search）

读取位置：`internal/tools/tavily.go`（优先环境变量 `TAVILY_API_KEY`，否则读 `config.json.web_search.tavily_api_key`）

- `web_search.tavily_api_key`：Tavily API Key（启用 `tavily_search/extract/crawl` 时必填）。

### 6.3 `assistant`（主 Agent 行为与风格）

读取位置：

- reply style：`cmd/agent/main.go` 的 `loadReplyStyleFromConfig`
- auto compaction：`cmd/agent/main.go` 的 `loadAutoCompactionPatchFromConfig` + `internal/agent/auto_compaction.go`

#### 6.3.1 `assistant.reply_style`

- `assistant.reply_style.enabled`：是否启用 reply style。
  - 若未设置：只要 `text` 或 `md_path` 任一存在，就视为启用（见 `loadReplyStyleFromConfig`）。
- `assistant.reply_style.md_path`：Markdown 文件路径（相对路径按 `config.json` 所在目录解析）。
  - 示例：`reply_style.md`
- （模板里未出现，但支持）`assistant.reply_style.text`：直接内联文本（优先级高于 `md_path`）。

#### 6.3.2 `assistant.auto_compaction`

用于“上下文溢出时自动总结+重试”。字段见 `internal/agent/auto_compaction.go` 的 `AutoCompactionConfigPatch`：

- `enabled`：是否启用（默认 true）
- `max_attempts`：最大重试次数（可设为 0 表示不做“总结+重试”，但仍会做 tool 输出硬截断）
- `keep_last_user_turns`：总结时保留最近 N 个 user turn（建议 >= 1）
- `summary_max_tokens`：总结调用最大 tokens
- `summary_max_chars`：summary 注入 system 的字符上限
- `summary_input_max_chars`：送给 summarizer 的 transcript 上限
- `hard_max_tool_result_chars`：任何时候对 tool 输出的硬截断上限
- `overflow_max_tool_result_chars`：溢出恢复时更激进的 tool 输出截断上限

### 6.4 `memory`（长期记忆）

读取位置：`internal/memory/config.go`（`LoadConfig`）+ `internal/memory/paths.go`（`ResolvePaths`）

- `memory.enabled`：是否启用记忆系统（默认 true）。
- `memory.workspace_dir`：记忆工作区根目录（支持 `~`）。
  - 默认：`~/.xinghebot/workspace`
  - 实际 memory root 会是：`<workspace_dir>/<project_key>/memory`
- `memory.project_key`：项目 key（用于区分不同项目的记忆目录）。
  - 为空时会自动派生（优先 git remote，再回退到 cwd hash；见 `internal/memory/paths.go`）。
- `memory.root_dir`：直接指定 memory root（优先级最高；非空则忽略 workspace_dir/project_key）。
- `memory.timezone`：时区（`Local` 或 IANA 名称）。
- `memory.backend`：后端类型（当前模板默认 `scan`；主要是扫描 Markdown 文件）。
- `memory.auto_load_memory_into_prompt`：是否把 `MEMORY.md` 自动注入主 Agent prompt。
- `memory.auto_update_memory_md`：是否在会话推进中自动更新 `MEMORY.md`（用于“把关键信息沉淀下来”）。
- `memory.memory_md_max_chars`：`MEMORY.md` 注入 prompt 的字符上限（保持紧凑）。
- `memory.auto_flush_on_compaction`：发生自动压缩（compaction）时，是否把会话摘要/关键点写入记忆文件。
- `memory.auto_capture_on_new_session`：启动新会话时是否自动 capture（写 sessions 记录）。
- `memory.auto_flush_on_session_capture`：capture 后是否自动 flush（把重要内容整理到 `MEMORY.md`/daily）。
- `memory.auto_daily_summary`：是否自动做每日总结（sessions → daily）。
- `memory.index_history_jsonl`：是否把历史索引写成 JSONL（一般保持 false）。
- `memory.max_results`：`memory_search` 默认最大返回条数（默认 10）。
- `memory.redaction`：脱敏（避免把 key 写进记忆）。
  - `memory.redaction.enabled`：是否启用（默认 true）
  - `memory.redaction.patterns`：命中即打码的关键片段（如 `sk-`、`tvly-`、`authorization_code` 等）

### 6.5 `autonomy`（Heartbeat / Cron）

读取位置：`internal/autonomy/config.go`

#### 6.5.1 `autonomy.enabled`

- 总开关。关闭后 cron/heartbeat runner 都不会启动（见 `cmd/agent/main.go` 的 `cronrunner.Start` / `heartbeatrunner.Start` 调用逻辑）。

#### 6.5.2 `autonomy.heartbeat`

读取位置：`internal/autonomy/heartbeatrunner/*` + tools `internal/tools/heartbeat.go`

- `enabled`：是否启用 heartbeat runner（注意：与 cluster heartbeat 不是一回事）。
- `every`：运行间隔（`time.ParseDuration` 语法，如 `30m`）。
- `coalesce_ms`：合并窗口（避免频繁触发）。
- `retry_ms`：失败重试等待。
- `path`：`HEARTBEAT.md` 的路径（相对路径按运行时 cwd 解析，runner 会做路径解析）。
- `ok_token`：当 `HEARTBEAT.md` 包含该 token 时，视为“无需动作/已 OK”（用于去重）。
- `dedupe_hours`：去重窗口（避免重复执行同一 heartbeat 任务）。

#### 6.5.3 `autonomy.cron`

读取位置：`internal/autonomy/cronrunner/*` + tools `internal/tools/cron*.go`

- `enabled`：是否启用 cron runner。
- `store_path`：cron jobs 存储路径（JSON）。
  - 为空时默认落在 memory workspace 下：`<workspace>/<project>/scheduler/cron/jobs.json`（见 `internal/autonomy/cron/paths.go`）。
- `default_timezone`：cron 解释 schedule 时的默认时区。
- `max_timer_delay`：tick 循环最大 sleep。
- `default_timeout`：每个 job 执行默认超时。
- `stuck_run`：卡住的 run 多久算 stuck（用于 reclaim）。
- `min_refire_gap`：最小重触发间隔（避免抖动）。
- `email_to`：cron 结果邮件默认收件人（需要 gateway 配置可用）。
- `email_subject_prefix`：cron 邮件主题前缀（默认 `[Cron]`）。

### 6.6 `start_params`（把常用 CLI 参数写进 config，避免命令太长）

读取位置：`cmd/agent/start_params.go`（`loadStartParams`）+ `cmd/agent/master_slave.go`

#### 6.6.1 `start_params.master`

- `listen`：Master 监听地址（`host:port`）。
- `ws_path`：WebSocket 路径（默认 `/ws`）。
- `ui`：`tui` 或 `plain`。
- `redis_url`：可选，用于 presence/route（`internal/cluster/presence_redis.go`）。
- `heartbeat`：预期 slave 心跳间隔（字符串 duration，如 `5s`）。
- （模板里未出现，但代码支持）`chat_tool_mode`：`dispatcher` 或 `full`（见 `cmd/agent/master_slave.go`）。

#### 6.6.2 `start_params.slave`

- `name`：slave 展示名。
- `master`：master 的 ws 地址（如 `ws://127.0.0.1:7788/ws` 或 `wss://...`）。
- `heartbeat`：slave 向 master 发心跳的间隔。
- `max_inflight_runs`：并发执行 `remote_agent_run` 的上限。
- `insecure_skip_verify`：当使用 `wss://` 时是否跳过 TLS 校验（危险）。
- （模板里未出现，但代码支持）`id`：稳定 slave id。
- （模板里未出现，但代码支持）`tags`：逗号分隔 tags（`k=v,k=v`），用于调度/标识。

### 6.7 `cluster`（Master/Slave 鉴权、TLS、远程文件传输）

读取位置：`internal/cluster/config.go` + `cmd/agent/master_slave.go`

- `cluster.secret`：Base64 的共享密钥（Master 首次启动会自动生成并写回 `config.json`）。
  - Slave 必须使用与 Master 相同的 secret 才能注册。
- `cluster.tls`：
  - `enabled`：Master 是否用 TLS 提供 `wss://`。
  - `cert_file` / `key_file`：证书路径（当 enabled=true 必填）。
  - `insecure_skip_verify`：slave 连接时是否跳过证书校验（危险）。
- `cluster.files`（远程文件传输限制）：
  - `root_dir`：文件落地目录（默认 `.cluster/files`）。
  - `max_file_bytes`：单文件上限。
  - `max_total_bytes`：总上限。
  - `retention_days`：保留天数。
  - `chunk_size_bytes`：分片大小。
  - `max_inflight_chunks`：并发分片数。

### 6.8 `gateway`（Email Gateway）

读取位置：`internal/gateway/config.go`

- `gateway.enabled`：总开关（模板默认 false）。
- `gateway.email`：
  - `provider`：邮件服务商（默认 `126`，内置默认 IMAP/SMTP 主机）。
  - `email_address`：邮箱地址。
  - `authorization_code`：授权码（不是登录密码）。
  - `imap.server/port/use_ssl`：IMAP 配置。
  - `smtp.server/port/use_ssl`：SMTP 配置。
  - `poll_interval_seconds`：轮询收件箱的间隔。
  - `allowed_senders`：允许触发的发件人白名单（逗号/空格/换行分隔都可）。

### 6.9 `multi_agent`（子 Agent 上限与自动清理）

读取位置：

- `worker.max_turns`：`internal/multiagent/worker_config.go`
- `cleanup`：`internal/multiagent/auto_cleanup_config.go` + `cmd/agent/main.go`（启动清理后台任务）

#### 6.9.1 `multi_agent.worker.max_turns`

- 子 Agent worker 最大回合数上限（默认 40）。
- 作用：即使 `agent_spawn` 传了更大的 `max_turns`，也会被这个上限 cap（避免跑飞）。

#### 6.9.2 `multi_agent.cleanup`

用于定期归档/删除 `.multi_agent/runs` 里的已结束 run：

- `enabled`：是否启用（默认 true）。
- `mode`：`archive`（默认）或 `delete`（谨慎）。
- `interval_minutes`：清理检查间隔。
- `archive_after_minutes`：run 结束超过多久后才处理。
- `keep_last`：保留最近 N 个 run。
- `include_failed`：是否也处理 failed run（默认 false，便于保留排错证据）。
- `dry_run`：只预览不落盘。
- （模板里未出现，但代码支持）`archive_dir`：archive 目录；空时默认 `.multi_agent/archive`（见 `internal/multiagent/auto_cleanup_config.go`）。

### 6.10 `notes`

- 纯备注字段，用于解释模板含义；程序逻辑通常不读取它（安全）。

---

## 7) 修改 `config.json` 并让它生效（强烈建议按这个流程）

### 7.1 典型流程（推荐）

1. 修改前备份：`cp config.json config.json.bak`
2. 编辑 `config.json`（保持合法 JSON）。
3. 校验 JSON（任选其一）：
   - `python -m json.tool config.json >/dev/null`
   - `jq . config.json >/dev/null`
4. 触发生效：**重启进程**（三选一）：
   - 在 TUI 输入：`/restart`
   - 通过工具调用：`agent_restart`（可带 reason/note）
   - 手动退出后重新运行 `xinghebot ... --config config.json`

原因：LLM client、tool registry、system prompt、后台 runner 等大多在启动时装配；重启能保证一致性。

### 7.2 哪些改动不必重启？

- 修改 `mcp.json`：运行时调用 `mcp_reload` 即可（无需重启）。
- 安装/创建 skill：用 `skill_install/skill_create` 后，Agent 会自动 `ReloadSkills()` 更新 `<available_skills>`（见 `internal/agent/agent.go`）。

---

## 8) 二次开发 / 自我迭代（给“只有二进制”的用户）

### 8.1 GitHub 源码仓库地址（请你后续手动填上）

> TODO: 在这里填你的仓库地址，例如 `https://github.com/<OWNER>/<REPO>`
>
> - `GITHUB_REPO_URL = REPLACE_ME`

### 8.2 标准开发流程（Agent 可执行的命令模板）

1. clone 源码（用户电脑需已安装 `git`）：

```bash
git clone "$GITHUB_REPO_URL" xinghebot-src
cd xinghebot-src
```

2. 跑测试（如果用户想先确保环境 OK）：

```bash
go test ./...
```

3. 构建 dist 二进制（包含 `--init` 的内置资源）：

```bash
bash scripts/build_dist.sh
```

4. 如果你修改了 `internal/bootstrap/cmd/gen/main.go`（例如要把新 skill 打进 `--init` bundle），需要重新生成 bundle：

```bash
go generate ./internal/bootstrap
```

---

## 9) 常见配置/运行问题速查

- 报 “`config.json not found`”：先执行 `xinghebot chat --init`（或 `master/slave --init`）释放模板与 skills。
- Slave 报 “`cluster.secret missing/invalid`”：把 Master 生成的 `config.json.cluster.secret` 复制到 Slave 的配置里（或用同一份配置）。
- Tavily 工具报缺 key：设置环境变量 `TAVILY_API_KEY` 或填 `config.json.web_search.tavily_api_key`。
- MCP server 连接失败：检查 `mcp.json` 的 `command/args/env`，然后 `mcp_reload`；必要时看 `verify_integration.py`/`check_config.py`。

