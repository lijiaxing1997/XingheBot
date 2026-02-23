# 分布式 Master/Slave Agent 设计（Redis + WebSocket）

> 目标：在**同一套二进制**中，通过启动参数进入 `Master` 或 `Slave` 模式，实现跨机器的主从 Agent 协作；`Slave` 通过 WebSocket 常连接注册到 `Master`；`Master` 具备 **Redis + WS 寻路**能力（Real-time API Gateway）；并在 `Master` 的 TUI 左侧面板中展示 **Session 列表 + 在线 Slave 列表**；`Master` 侧以“工具（tool）”的形式调用 `Slave` 上的 Agent 并回传结果。

---

## 0. 需求理解确认（对齐）

你要的“分布式主从 Agent”核心是：

1. **两种连接模式**
   - **模式 1（自动部署）**：`Master` 侧通过一个 *skill* 引导/自动化：询问 SSH 地址/账号/密码（或 key），用 `scp` 把编译好的二进制推到远端，再用 `ssh` 执行启动命令把远端进程拉起来进入 `Slave` 模式。
   - **模式 2（手动部署）**：用户自行把二进制放到其他服务器并手动运行：`Master` 用 `Master` 参数启动；远端用 `Slave` 参数启动并带上 `Master 地址/端口/密钥` 与 `自身代号`；`Slave` 主动发起 WS 连接到 `Master` 完成注册并保持长连接。
2. **Master 具备 Redis + WS 寻路（API Gateway）**
   - `Slave` 与 `Master` 之间的实时通道是 WS；
   - `Redis` 用于 **在线状态/路由**（以及未来多 Master 实例横向扩展时的跨实例转发）。
3. **TUI 左侧面板拆分**
   - 上半：仍显示 Session（现有 multi-agent run/session）；
   - 下半：显示在线 `Slave`（代号/称呼 + 在线状态/最后心跳等）。
4. **同一二进制，不同启动参数进入不同状态**
5. **Master Agent 调 Slave Agent（以工具体现）**
   - `Master` 上的主 Agent 需要能“调用”某个 `Slave` 上的 Agent 执行任务；
   - 执行完的结果回传到 `Master`，在 `Master` 的视角里这是一次 tool call 的返回值。

以上我理解一致；下面是拆解、协议与实现计划（偏健壮性设计）。

---

## 1. 总体架构

### 1.1 组件

- **Master（主程序）**
  - 交互入口（现有 `chat --ui=tui` 的体验可复用）
  - WebSocket Server：接受 `Slave` 注册并维持连接
  - Redis：维护在线状态、路由信息；支持多 Master 实例时跨实例寻路/转发
  - Tool：`remote_agent_run`（或类似命名），把 “在某个 Slave 上跑一次 agent 任务” 作为工具暴露给 LLM
- **Slave（子程序）**
  - WebSocket Client：连接 `Master`，注册（含密钥/代号/能力/版本）
  - Remote Agent Runner：接受 `Master` 下发的“任务请求”，本机执行并回传结果
- **Redis（控制面）**
  - Presence：在线列表/状态 TTL
  - Route：`slave_id -> owning_master_instance_id`（为“Redis + WS 寻路”提供支撑）
  - Forwarding（可选）：跨 Master 实例转发消息（Pub/Sub 即可；更强需求可升级 Streams）

### 1.2 数据流（单 Master 实例）

```
┌──────────────┐        WS        ┌──────────────┐
│ Master       │<────────────────>│ Slave         │
│ - TUI + Agent│                  │ - AgentRunner │
│ - Tool: call │                  │ - Tools/Exec  │
│ - WS server  │                  │               │
│ - Redis      │                  │               │
└──────┬───────┘                  └──────────────┘
       │
       │ Presence / Route
       ▼
    ┌───────┐
    │ Redis │
    └───────┘
```

### 1.3 数据流（多 Master 实例，Redis 寻路价值所在）

> 说明：**本次先不实现跨实例转发（Pub/Sub）**，只做单 Master 实例；这里保留是为了后续“会议/组织所有子程序”的多 Master 演进不需要推倒重来。

```
   Tool call on Master-B
          │
          ▼ (lookup route in Redis)
  slave_id -> owning Master-A
          │
          ▼ (publish forward msg)
      Redis Pub/Sub
          │
          ▼
     Master-A  ─── WS ───>  Slave
          ▲
          └────── publish response back to Master-B (reply_to)
```

> MVP 可以只跑 1 个 Master 实例，但协议/Redis 结构预留多实例能力，避免后期重构。

---

## 2. 运行形态与 CLI 设计（同一二进制）

> 目标：保持“同一源码编译出的二进制”，仅通过子命令/参数切换。

建议新增两个子命令（或在现有 `chat` 基础上加 `--mode` 也可；这里按可读性选子命令）：

### 2.1 Master

```bash
agent master \
  --config config.json \
  --ui=tui \
  --listen 0.0.0.0:7788 \
  --ws-path /ws \
  --redis-url redis://127.0.0.1:6379/0
```

职责：
- 启动 TUI + 主 Agent（复用现有 chat 逻辑）
- 并行启动 WS Gateway（接受 Slave）
- 初始化 Redis 客户端与路由器（Router）
- 把 `remote_agent_run` / `remote_slave_list` 注册到 `tools.Registry`
- **启动时确保 `cluster.secret` 存在**：若 config 内没有，则 Master 自动生成并写回 `config.json`（见 §6.1）

### 2.2 Slave

```bash
agent slave \
  --config /path/to/slave-config.json \
  # TLS 启用时用 wss://，否则用 ws://
  --master wss://MASTER_IP:7788/ws \
  --name build-linux-amd64 \
  --id slave-01 \
  --tags "os=linux,arch=amd64,zone=ali-hz" \
  --heartbeat 5s
```

职责：
- 主动连到 Master
- 注册（包含 `id/name/meta/capabilities/version`）
- 保活、断线重连（指数退避 + 抖动）
- 接收 `agent.run` 并在本机执行，回传 `agent.result`
- **无本地交互输入**：只接受 Master 下发的调用请求（见 §6.3）

---

## 3. WebSocket 协议（消息 + 健壮性）

### 3.1 基础约束

- 传输格式：JSON（**单帧一条消息**；必要时支持 chunk/stream）
- 必须字段：`type`、`id`（请求/响应关联）、`ts`、`payload`
- 协议版本：`protocol_version`（便于兼容升级）
- 最大消息大小：建议 1–4MB（防止一次性输出把内存/Redis/日志打爆）

统一 envelope：

```json
{
  "type": "register",
  "id": "msg-uuid",
  "ts": 1730000000,
  "protocol_version": 1,
  "payload": {}
}
```

### 3.2 注册流程

1) `Slave -> Master`: `register`

payload 建议字段：
- `slave_id`：稳定 ID（允许用户指定；否则自动生成后落盘）
- `name`：展示名（TUI 显示）
- `auth`：HMAC 签名（见 §6.1，避免明文 token）
- `version`：二进制版本（`appinfo`）
- `capabilities`：例如 `["remote_agent_run"]`
- `meta`：OS/ARCH/hostname/tags（用于选择/筛选）

2) `Master -> Slave`: `register_ack`
- `accepted` / `reason`
- `heartbeat_interval`
- `server_instance_id`

3) 在线维护：
- `heartbeat`：Slave 定期发；Master 回 `heartbeat_ack`（或直接依赖 WS ping/pong）
- Master 在 Redis 刷新 presence TTL

边界情况（必须处理）：
- **重复 slave_id**：默认“后连的踢掉先连的”，并在日志记录来源（IP/UA/meta）。
- **认证失败**：立即 close 连接，避免资源占用。
- **版本不兼容**：返回 `register_ack(accepted=false, reason=...)`。

### 3.3 任务执行（Remote Agent Run）

`Master -> Slave`: `agent.run`

```json
{
  "type": "agent.run",
  "id": "req-uuid",
  "payload": {
    "task": "在 /var/log 下查找 ...",
    "options": {
      "max_turns": 40,
      "temperature": 0.2,
      "max_tokens": 0,
      "timeout_seconds": 900
    },
    "metadata": {
      "initiator_run_id": "run-...",
      "initiator_agent_id": "primary"
    }
  }
}
```

`Slave -> Master`: `agent.result`

```json
{
  "type": "agent.result",
  "id": "req-uuid",
  "payload": {
    "status": "completed",
    "output": "....",
    "error": "",
    "duration_ms": 123456
  }
}
```

可选增强（建议预留但可分期实现）：
- `agent.event`：进度事件（例如 tool 调用开始/结束、阶段性日志）。Master 可用于 debug 或在 TUI 显示“正在执行/最近动作”。
- `agent.cancel`：Master 发送取消；Slave 要能中止当前执行（context cancel + 杀子进程）。
- 并发限制：Slave 同一时刻最多 N 个 in-flight（默认 1），超限返回 `busy`。

---

## 4. Redis 设计（Presence + Route + Forward）

> “Redis + WS 寻路”的最小闭环：Redis 记录 `slave_id -> owning_master_instance_id`，让任意 Master/工具都能把消息转发到**握着 WS 连接**的那台 Master。

### 4.1 Key 设计（建议）

- Presence（JSON + TTL）：
  - `gateway:slave:<slave_id>` = JSON（name, status, last_seen, meta, version, owner_instance）
  - TTL：例如 15s（Slave 心跳 5s 刷新一次）
- Route（多 Master 实例寻路）：
  - `gateway:route:<slave_id>` = `<master_instance_id>`（同 TTL）

### 4.2 跨实例转发（建议 MVP 用 Pub/Sub）

> 本次不实现（后续需要“多 Master 组织会议/统一调度”时再做）。

Redis Pub/Sub channel：
- `gateway:to_master:<master_instance_id>`

forward message envelope（示意）：

```json
{
  "type": "forward.to_slave",
  "reply_to": "master-b",
  "slave_id": "slave-01",
  "ws_msg": { "type": "agent.run", "id": "req-uuid", "payload": { "...": "..." } }
}
```

响应回传同理（回到 `reply_to`）：

```json
{
  "type": "forward.to_master",
  "to": "master-b",
  "ws_msg": { "type": "agent.result", "id": "req-uuid", "payload": { "...": "..." } }
}
```

说明：
- **不做离线队列/离线投递**：Slave 离线时，`remote_agent_run` 直接失败返回即可。
- 因此 **Redis Streams 不是必需项**；后续真需要再评估。

---

## 5. Master 侧“工具化调用 Slave”的设计

### 5.1 工具形态（推荐：单工具 + 参数）

避免“每个 Slave 动态注册一个 tool（工具列表膨胀）”，建议提供两个工具：

1) `remote_slave_list`
- 用途：让模型/用户能看到在线 Slave 列表与状态；也可给 TUI 复用数据源。

2) `remote_agent_run`
- 用途：在指定 Slave 上执行一次 agent 任务并返回结果（满足你的第 5 点：Master 上体现为一个工具）。

### 5.2 `remote_slave_list`（返回在线列表）

输入（示意）：

```json
{ "query": "linux", "only_online": true }
```

输出（示意）：

```json
{
  "slaves": [
    { "slave_id": "slave-01", "name": "build-linux-amd64", "status": "online", "last_seen": "..." },
    { "slave_id": "slave-02", "name": "gpu-box", "status": "offline", "last_seen": "..." }
  ]
}
```

### 5.3 `remote_agent_run`（核心）

输入（示意）：

```json
{
  "slave": "slave-01",
  "task": "在远端机器上执行 ... 并把结果整理为 5 条要点",
  "options": {
    "max_turns": 40,
    "temperature": 0.2,
    "max_tokens": 0,
    "timeout_seconds": 900
  }
}
```

输出（示意）：

```json
{
  "slave_id": "slave-01",
  "request_id": "req-uuid",
  "status": "completed",
  "output": "....",
  "error": "",
  "duration_ms": 123456
}
```

实现要点（健壮性）：
- Master tool call 侧用 `context` + `timeout_seconds` 控制等待
- 输出大小做硬截断（例如 200k chars），避免一次返回打爆上下文
- 出错要区分：
  - `offline`（Slave 不在线）
  - `timeout`
  - `remote_error`（Slave 执行失败）
  - `protocol_error`

### 5.4 同步 vs 异步

MVP 可以先做“同步等待 `agent.result` 再返回 tool output”，因为你说“工具体现 + 回传结果”。

如果后续要更强（长任务/可取消/可查看进度），可以升级为：
- `remote_agent_run_async`：立即返回 `request_id`
- `remote_agent_wait` / `remote_agent_progress` / `remote_agent_cancel`

---

## 6. 安全与密钥（必须提前想清楚）

### 6.1 `cluster secret`（连接认证）

约束（已确认）：
- **密钥由 Master 生成**，并保存到 `config.json`。
- `Slave` 必须拥有相同的 `cluster.secret` 才能注册成功（手动复制/自动部署 skill 下发）。

认证方式（已确认，避免明文 token）：
- `register` 消息里不传 `token`，而是传签名：
  - `sig = hex(HMAC_SHA256(secret, slave_id + "\n" + ts + "\n" + nonce))`
  - `ts`：Unix 秒
  - `nonce`：随机字符串（建议 16+ bytes hex/base64）
- Master 校验：
  - `ts`：允许 ±60s（可配置）
  - `nonce`：短期去重（内存或 Redis SET，TTL≈2–5min）防重放
  - `sig`：常量时间比较

Master 生成/落盘策略（建议）：
- 若 `config.json` 缺少 `cluster.secret`：
  - 生成 32 bytes 随机数
  - base64 存储到 `cluster.secret`
  - 原子写回 `config.json`（并尽量把权限设为 `0600`）

### 6.2 通道安全（TLS，可配置）

约束（已确认）：
- 是否启用 TLS 由 `config.json` 控制；启用后 Master 需要证书文件路径（通常自签）。
- 自签证书场景下，Slave 侧“出现告警/不受信任”是正常现象：实现上等价于**允许跳过证书校验**继续连接。
- 说明：`cert_file/key_file` 是 Master（服务端）使用；`insecure_skip_verify` 是 Slave（客户端）连接时使用。

配置建议（示意，字段名可实现时再定）：
```json
{
  "cluster": {
    "tls": {
      "enabled": true,
      "cert_file": "certs/server.crt",
      "key_file": "certs/server.key",
      "insecure_skip_verify": true
    }
  }
}
```

安全提醒（强烈建议写进最终文档/帮助里）：
- `insecure_skip_verify=true` 会让 Slave 容易被中间人攻击（MITM）。更安全的做法是：
  - 提供 `ca_file`（让 Slave 信任该自签 CA），或
  - 做证书指纹 pinning（更复杂，但最稳）。

### 6.3 Slave 需要 LLM API Key（已确认）

约束（已确认）：
- Slave 是独立节点：**自身持有 LLM API Key**（在 `config.json`），具备 skill、MCP、内置工具等完整能力。
- Slave **不接受本地用户输入**，只接受 Master 下发请求。
- 被调用后，Slave 也以“主 Agent + 子 Agent（多进程）”方式执行任务：主 Agent 负责拆解/编排，子 Agent 负责具体执行。

实现形态建议（满足“像主程序一样会创建子 Agent”）：
- Slave 收到 `agent.run`：
  1) 创建本地 run（例如 `.multi_agent/runs`）
  2) 启动“本地 primary 编排 Agent”（headless，无 UI）
  3) 由 primary 创建/管理子 Agent 执行，并**等待完成后**汇总输出
  4) 回传 `agent.result` 给 Master（作为 `remote_agent_run` 的 tool 返回值）

备注（安全边界）：
- 自动部署模式（模式 1）若选择下发完整 `config.json`，等同于分发 API Key；需要你在运维侧接受该风险并做好权限/审计/隔离。

---

## 7. Master TUI 改造（左侧 Session + Slaves）

当前 `tui.go` 左侧是 `renderSessions(width, height)` 一整个面板。你的需求是“同一面板上下拆两块”：

### 7.1 布局建议

- 左侧 panel 高度 `H`，拆为：
  - `Sessions`：`Hs`（最小 8 行，优先保证可用）
  - `Slaves`：`H - Hs`（最小 6 行，不足则只显示标题 + 1-2 行）
- Sessions 区保持现有行为（新建/切换/删除提示）
- Slaves 区显示：
  - `● online` / `○ offline`（或颜色）
  - `name (slave_id)`（name 优先展示；宽度不够时截断）
  - `last_seen`（可选，或显示 `5s ago`）

### 7.2 数据刷新

建议 TUI tick（当前已有 `tuiTickCmd()`）中追加：
- 从 Master 进程内的 `SlaveRegistry` 拿快照（内存）  
  或
- 从 Redis 读 presence（如果你希望 TUI 不依赖本地内存；但 Redis 读会更频繁）

MVP 推荐：**内存 registry 为准，Redis 为同步/跨实例**，TUI 读内存即可。

### 7.3 交互（先做展示即可）

约束（已确认）：只展示在线 Slave，不做可选中/可操作；调度由 LLM 通过 `remote_slave_list` + `remote_agent_run` 完成。

---

## 8. 连接模式 1：自动部署 Skill 设计（按 Skill Creator 思路）

> 这里是“给模型的可复用工作流”，最终落地到仓库的 `skills/<skill-name>/SKILL.md`（以及可选脚本）。

### 8.1 Skill 命名（建议）

- skill 目录名：`ssh-deploy-slave`
- 触发描述（frontmatter 的 `description` 要写清楚触发词）：  
  当用户提到“ssh 部署 / scp 推送二进制 / 远程启动 slave / 一键部署 slave / 批量部署到多台 Linux 服务器”等时使用。

### 8.2 模式 1 的标准工作流（要健壮）

1) **收集参数（必须问清楚）**
   - 目标主机：`host`、`port`（默认 22）
   - 认证方式：支持 `ssh key` 与密码两种
     - key：推荐默认路径 `~/.ssh/id_rsa` / `~/.ssh/id_ed25519` 或用户指定
     - password：若要全自动需要 `sshpass`；否则只能走交互式输入
   - 远端目录：例如 `~/agent-bin/` 或 `/opt/agent/`
   - 远端运行用户：是否需要 `sudo`
   - Slave 注册信息：`slave_id`、`name`、`tags`
   - Master WS 地址：`ws://.../ws`（或 `wss://...`）
   - cluster secret：来自 `config.json`（`cluster.secret`），避免把 key 写进命令行历史
   - 是否需要同时部署 Slave 的 `config.json` / `skills/`（Slave 需要 LLM key，见 §6.3）
2) **构建正确架构的二进制**
   - Linux 服务器常见 `amd64/arm64`
   - 仓库已有脚本：`scripts/build_dist.sh`（产物在 `dist/`）
3) **传输**
   - 推荐 `scp` 或 `rsync -e ssh`
   - 传输后校验：`sha256sum`（可选）
4) **启动**
   - 简单方式：`nohup ./agent slave ... > slave.log 2>&1 &`
   - 更健壮：写 `systemd` unit（自动重启、开机自启、集中日志）
5) **验证**
   - `ssh` 查看进程（`ps/pgrep`）
   - Master 侧 `remote_slave_list` / TUI 出现在线节点

### 8.3 Skill 资源规划（可复用脚本）

建议 skill 内置脚本（未来实现时）：

- `skills/ssh-deploy-slave/scripts/deploy.sh`
  - 参数化：`--host --user --port --key/--password --bin --remote-dir --start-cmd ...`
  - 尽量不回显密码/secret
  - 自动创建目录、上传、写 systemd（可选）、启动与检查

如果要支持“密码自动化”：
- 依赖 `sshpass`（Linux/macOS 可装；但不是默认具备）
- skill 行为建议：
  - 检测到未安装 `sshpass`：提示用户安装或切换到 key/交互模式
  - 任何情况下都避免在日志/回显中输出明文密码与 `cluster.secret`

### 8.4 示例（用户视角）

用户对 Master 说：
- “帮我把 slave 部署到 `10.0.0.12`，用户名 `root`，端口 `22`，slave 名称 `build-01`，master 地址是 `wss://x.x.x.x/ws`”

skill 引导收集后执行：
- 构建：`bash scripts/build_dist.sh`
- 上传：`scp dist/agent-linux-amd64 root@10.0.0.12:/opt/agent/agent`
- 启动：`ssh root@10.0.0.12 'nohup /opt/agent/agent slave ... &'`

---

## 9. 实现拆解与里程碑（不含具体编码）

> 目标是“先跑通闭环，再逐步健壮化”，每一步都可验证。

### Phase 1：协议与模块骨架（本地）

- 定义 `internal/cluster/protocol.go`（消息 struct + 常量）
- 定义 `SlaveInfo` / `SlaveRegistry`（内存）
- `Master` 启动 WS server，`Slave` 能连接并完成 `register/register_ack`

验收：
- 本机起两个进程：Master/Slave，TUI/日志看到注册成功

### Phase 2：Redis Presence + Route

- 接入 Redis client
- Master 在 register/heartbeat 时写 presence + route（TTL）
- `remote_slave_list` 从 registry/Redis 能列出在线节点

验收：
- Redis 中能看到 key；断开 Slave 后 TTL 过期，状态变 offline

### Phase 3：跨实例转发（Pub/Sub）

（本次不实现，未来演进项）

### Phase 4：Remote Agent Runner（满足第 5 点）

- Slave 侧实现 `agent.run` handler（已确认：Slave 自带 LLM key，且“主 Agent + 子 Agent”执行）：
  - 收到请求 -> 创建本地 run -> 启动编排 primary -> 由 primary spawn 子 Agent -> 等待完成 -> 汇总结果 -> 回传
- Master 侧实现 `remote_agent_run` 工具：请求/等待响应/超时处理/输出截断

验收：
- Master 输入一条触发 tool 的任务，能在 Slave 上执行并把结果回传

### Phase 5：TUI 左侧面板改造

- `renderSessions` 拆成 `renderLeftPanel`（Sessions + Slaves）
- tick 刷新 slaves 状态并渲染

验收：
- 左侧下半能看到在线 Slave 名称/状态

### Phase 6：模式 1 自动部署 skill 落地

- 新增 `skills/ssh-deploy-slave/`（SKILL.md + scripts）
- 脚本支持：构建/上传/启动/验证

验收：
- 通过 skill 一键把 slave 拉起来并注册成功

### Phase 7：硬化与测试

- WS 协议测试：`httptest` + websocket client
- Redis 测试：可用 `miniredis` 或 CI 中起真实 redis（取决于你项目习惯）
- 失败路径：断线重连、超时、重复注册、无效 token
- 资源控制：并发上限、最大输出、日志滚动

---

## 10. 决策记录（已确认）

本轮已确认的决策：
1) `cluster.secret`：Master 生成并写入 `config.json`；注册鉴权使用 `HMAC(secret, slave_id + ts + nonce)`。
2) TLS：由 `config.json` 控制；启用时提供证书路径；允许 Slave 跳过自签证书校验继续连接（同时在文档中强提示风险）。
3) Slave：自带 LLM API Key；无本地交互输入；被调用后同样“主 Agent + 子 Agent”执行。
4) 多 Master 跨实例转发：未来演进项，本次不做；离线不投递，离线直接失败返回。
5) TUI：Slave 列表仅展示，不做可操作。
6) 自动部署：同时支持 key 与密码；密码全自动依赖 `sshpass`（否则走交互/提示安装）。
