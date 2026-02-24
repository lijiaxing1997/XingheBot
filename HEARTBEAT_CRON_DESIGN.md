# 主动思考与定时任务方案（Heartbeat + Cron）— xinghebot / test_skill_agent

> 状态：可落地方案（不含实现）  
> 更新时间：2026-02-24  
> 目标：在 **无人发消息** 的情况下，依然能“主动提醒/汇报/跑一次后台思考”，且不牺牲本项目 **CGO_ENABLED=0 的交叉编译体验**。

本方案借鉴 OpenClaw 的思路：**Heartbeat（周期性 agent 回合）** + **Cron（持久化调度器）** 两套机制协作，并通过一个轻量的 **System Events 队列**把“外部事件/到点任务触发”喂给下一次 agent 回合，从而实现“没人发消息也能主动输出对你可见的内容”。

---

## 0. 术语与边界（避免与集群心跳混淆）

本仓库已有 “master/slave 心跳” 用于节点在线与 presence（见 `cmd/agent/master_slave.go`、`internal/cluster/slave_client.go`、配置 `start_params.*.heartbeat`）。  
本文的 **Heartbeat** 指的是 **Agent Heartbeat（主动思考心跳）**，两者必须在命名与配置上严格区分：

- **Cluster Heartbeat**：slave → master 的 keepalive（已有；不要改语义）
- **Agent Heartbeat**：master/chat 进程里，周期性触发一次“后台 agent turn”（本文新增）

本文把 sessionKey 统一定义为：**`run_id`**（即 `.multi_agent/runs/<run_id>/...` 对应的会话/Session）。

---

## 1. 现有代码上下文（关键锚点）

为保证设计能直接落到本项目现有结构上，下面是我们当前可以复用/挂载的关键点：

### 1.1 会话与对话持久化（run_id / history.jsonl）

- 主会话（primary）对话存储：`history.jsonl`
  - 交互式 TUI：`internal/agent/tui.go`（`tuiHistoryFileName = "history.jsonl"`）
  - headless slave：`cmd/agent/master_slave.go`（`headlessPrimaryHistoryFile = "history.jsonl"`）
- 单次“turn 执行器”：`internal/agent/tui.go` 的 `runTUITurnStreaming(...)`
  - 会构造 `systemMsg` + `sessionMsg(run_id=...)` + baseHistory + user turn
  - 在 compaction 时自动触发 `memory_flush`（已落地，见 `internal/agent/tui.go`）

### 1.2 “后台定时器”已有先例

- 多 Agent run 清理 sweeper：`multiagent.StartAutoCleanup(...)`（`internal/multiagent/auto_cleanup.go`）
  - 由 `newAgentRuntime(...)` 启动（`cmd/agent/main.go`），使用 `time.NewTicker` 周期执行

### 1.3 并发与跨进程文件锁（跨平台）

- `internal/multiagent/coordinator.go`：
  - `writeJSONAtomic(...)`：临时文件 + rename（跨平台）
  - `withFileLock(...)`：`O_EXCL` lock 文件（跨平台）
  - JSONL 追加/尾读工具：appendSequencedJSONL / readSequencedJSONL

这些能力可以直接复用到 Cron store / run log / 状态持久化上，且不引入 CGO。

---

## 2. 总体目标与非目标

### 2.1 目标（必须满足）

1) **无人交互也能“说话”**：到点提醒、后台任务完成、系统事件汇报都能自动进入下一次 agent 回合并输出用户可见消息。  
2) **鲁棒性**：
   - 进程挂起/系统时间跳变后能恢复（不会死循环、不漂移到永远不触发）
   - store 原子写；并发读写有锁；任务卡死有超时与“stuck 清理”
3) **降噪与成本控制**：
   - coalesce 去抖合并唤醒，避免频繁调用模型
   - 心跳无事时不污染 transcript（等价于“HEARTBEAT_OK”则不写入 history）
4) **不破坏交叉编译**：
   - `CGO_ENABLED=0` 仍可构建
   - 仅使用标准库 + 纯 Go 依赖（如需 cron 表达式解析）

### 2.2 非目标（第一版不做/可延后）

- 不做“多 master 分布式一致性调度”（先假设单 master 进程负责 Cron）
- 不保证在进程完全退出期间“也能推送”（Cron/Heartbeat 都依赖进程运行；但重启后应能 catch-up）
- 不做复杂日历/ICS 集成（可用 Cron + 系统事件先覆盖 80%）

---

## 3. 三大组件：System Events + Heartbeat + Cron

### 3.1 System Events（桥梁：把“触发”喂给下一次模型调用）

**定位**：一个非常轻量的队列（默认内存；可选最小持久化），按 `run_id` 分桶。  
生产者包括：Cron due、hook、后台任务完成、外部网关事件等。  
消费者包括：Heartbeat 执行器（会 drain 并注入 prompt）。

#### 3.1.1 数据模型（建议）

```go
type SystemEvent struct {
  RunID      string    // session key
  Kind       string    // "cron" | "exec" | "hook" | "notice" | ...
  ContextKey string    // 用于去重/溯源，例如 "cron:<job_id>"
  Text       string    // 要注入 prompt 的正文（短文本）
  CreatedAt  time.Time // UTC
}
```

#### 3.1.2 队列语义（鲁棒 + 降噪）

- **容量**：每个 run_id 最多 N 条（建议 20）；超出丢最老
- **连续重复去重**：若新事件 `Text` 与队尾完全相同则跳过
- **Drain 一次性消费**：`Drain(runID)` 返回当前队列并清空
- **Peek（可选）**：用于 Heartbeat “是否需要跑” 的预判（不清空）

> 关键点：System events 本质是“喂给下一轮思考的临时上下文”，不应长期留在 transcript 中（否则噪声会积累）。

#### 3.1.3 注入到 prompt 的位置

建议在 `runTUITurnStreaming(...)` 构造 `reqMessages` 时插入：

- `systemMsg`（全局 system prompt）
- `sessionMsg`（run_id 注入）
- **systemEventsMsg**（本次 drain 的系统事件摘要，system role）
- baseHistory（已持久化的对话上下文）
- user turn

这样可以保证：

- 系统事件一定在本次模型调用可见
- 事件不会因为 history 裁剪（auto_compaction）被吞掉
- 事件被 drain 后不会重复出现

#### 3.1.4 事件文本格式化（建议）

格式要稳定、可扫描，并含时间（UTC 或本地都可，但要统一）：

```
[System Events]
- 2026-02-24T09:00:00Z kind=cron key=cron:job-123
  text: ...
- 2026-02-24T09:10:00Z kind=exec key=exec:xyz
  text: ...
```

同时做硬限制：

- 单条 `Text` 上限（例如 4k chars）
- 总注入上限（例如 12k chars，超出截断并提示“已截断”）

---

### 3.2 Heartbeat（主动思考：周期性后台 agent 回合）

Heartbeat 分成两层：**调度层（何时跑）** + **执行层（跑什么，怎么降噪）**。

#### 3.2.1 生命周期（建议挂载点）

Heartbeat 属于“长期后台服务”，建议由 `newAgentRuntime(...)` 或 `runMaster/runChat` 启动与关闭：

- 参照 `multiagent.StartAutoCleanup(...)` 的模式：传入 `context.Context`，goroutine 持续运行，退出时 cancel。

#### 3.2.2 调度层：Interval + Wake 合并器

目标：既能“到点自发跑”，也能被外部触发立刻跑，但要 **合并去抖**。

1) **Interval timer**
- 每隔 `heartbeat.every` 触发一次
- 触发时不要直接调用模型：只做 `requestHeartbeatNow(reason="interval")`

2) **Wake 合并器（coalesce）**
- API：`RequestHeartbeatNow(runID, reason, coalesceMs)`
- 默认 `coalesceMs=250ms`：同 run_id 在 coalesce 窗口内只触发一次
- **优先级**（建议）：`manual/cron/hook/exec` > `interval`
- **忙碌重试**：如果该 run 正在处理用户 turn（TUI 的 `isRunBusy(runID)` 为 true），则把 wake 标记为 pending，并 `retryMs` 后再试

> 在本项目里，“是否忙碌”可直接复用 `internal/agent/tui.go` 的 busyRuns 逻辑（同一 run 不允许并发 turn）。

#### 3.2.3 执行层：runHeartbeatOnce（成本/噪声控制）

单次 Heartbeat 的核心目标：**只有在“有事”时才产出用户可见内容**。

建议的判定顺序：

1) **全局开关**：`autonomy.heartbeat.enabled`
2) **会话忙碌**：run busy → `skipped: busy`（由 Wake 重试机制再调度）
3) **静默时段**（可选）：`active_hours`（不在 active hours 则仅保留系统事件，延后投递）
4) **System Events 是否非空**：`Peek(runID)`；有则必须跑（cron/hook/exec 不能漏）
5) **HEARTBEAT.md 是否有效为空**：若无系统事件且 HEARTBEAT.md “有效内容为空”则跳过（省钱）

##### HEARTBEAT.md（控制“主动思考内容”）

建议沿用文件驱动，原因：

- 可版本控制/可手工编辑/无需改代码
- 跨平台、零依赖、与交叉编译无关

默认路径建议：

- 优先：项目根目录 `HEARTBEAT.md`
- 可配置：`autonomy.heartbeat.path`

“有效内容为空”的判定（建议与 OpenClaw 一致的思路）：

- 去掉标题、空 checklist、纯注释后无内容 → 视为空

##### Heartbeat prompt（建议）

Heartbeat 不是“随便跑一轮”，而是明确指令 + 明确 noop 输出：

- 要求读取 HEARTBEAT.md
- 要求处理本次注入的 `[System Events]`
- 若无任何需要对用户说的内容，输出固定 token：`HEARTBEAT_OK`

并注入当前时间（减少定时提醒类的不确定性）：

- `Current time (UTC): 2026-02-24T...Z`
-（可选）本地时间与时区

#### 3.2.4 降噪：不污染 history / 不重复 nag

为了不让 Heartbeat 把对话刷屏，建议：

1) **输出为 HEARTBEAT_OK**：
- 不写入 `history.jsonl`
- 不写入 UI（TUI 不展示）

2) **重复提醒去重**（建议）
- 对 run 维度记录 `last_heartbeat_text` + `last_heartbeat_sent_at`
- 24h 内若完全相同则跳过（避免“每天都重复一句废话”）
- 存放位置建议：run 的 `ui_state.json` 扩展字段（复用 `withFileLock`）

---

### 3.3 Cron（持久化调度器：可重启、可观测、可补偿）

Cron 负责 **准点触发**；触发后通过 system events + heartbeat 把内容变成用户可见消息，或触发一次“隔离会话 agentTurn”。

#### 3.3.1 Job 能干什么（两个 sessionTarget）

1) `session_target: "main"`（主会话提醒）
- payload: `kind="systemEvent"`
- 执行：`EnqueueSystemEvent(run_id, text, contextKey="cron:<job_id>")` + `RequestHeartbeatNow(run_id, reason="cron:<job_id>")`

2) `session_target: "isolated"`（隔离会话跑一次 agent turn）
- payload: `kind="agentTurn"`（包含 message / max_turns / model overrides / thinking（如有）/ timeoutSeconds 等）
- 执行：创建新的 run（metadata: source=cron, job_id=...），在该 run 内运行一次 headless turn
- 投递：可选择把最终输出再 enqueue 到某个 main run，并唤醒 heartbeat（或仅保存在该 isolated run 供事后查看）

> isolated 的价值：定时跑“后台检查/汇总/生成报告”，避免污染主会话上下文。

#### 3.3.2 Store（必须持久化）

建议路径（与 memory 一致，按 project_key 隔离）：

```
~/.xinghebot/workspace/<project_key>/scheduler/
  cron/
    jobs.json
    runs/<job_id>.jsonl
```

配置项：

- `autonomy.cron.store_path`（允许覆盖；默认如上）

写入要求：

- 原子写：临时文件 + rename（复用 `writeJSONAtomic`）
- 锁：`withFileLock(storePath+".lock", ...)`（防并发）
- 兼容/迁移：store 内含 `version` 字段，读取时做归一化，必要时回写

#### 3.3.3 调度层：单 timer + clamp + reload

目标：避免漂移、避免时间跳变导致“永远不触发”，避免死循环。

建议算法（对齐 OpenClaw timer.ts 思路）：

- `armTimer()`：
  - 找到所有 enabled job 中最小的 `next_run_at`
  - `delay = next - now`
  - **clamp**：`delay = min(delay, 60s)`（MAX_TIMER_DELAY），保证系统挂起后能快速恢复重算
  - setTimer(delay)

- `onTimer()`：
  - `ensureLoaded(forceReload=true)`：每个 tick 重新读 store（支持外部修改）
  - 找出 due jobs（`next_run_at <= now` 且非 running）
  - 对每个 due job：
    - 先标记 `running_at=now` 并持久化（防重复执行）
    - 带总超时执行（默认 10min，可 job override）
    - 执行完 `applyJobResult()`：
      - `kind="at"` one-shot：无论 ok/error 都 disable（避免 at 在过去导致死循环）
      - error：指数退避（30s → 1m → 5m → 15m → 60m）
      - cron：计算 next，并加 `min_refire_gap=2s` 防同一秒重复触发
  - stuck 清理：若 job running 超过阈值（例如 2h）则清掉 running 标记并 backoff

#### 3.3.4 Cron 表达式解析（交叉编译友好）

选择一个 **纯 Go** cron 解析库（例如 `robfig/cron/v3`），并明确：

- 支持时区：job 可带 `timezone`（默认 UTC 或 config 默认）
- 明确秒级支持与否（建议先不做秒级，减少边缘情况）

> 注意：只要依赖是纯 Go，就不会破坏 `CGO_ENABLED=0` 的交叉编译。

#### 3.3.5 可观测性（必须）

1) **事件流（进程内）**：added/updated/started/finished/skipped/error  
2) **run log（持久化 JSONL）**：每次执行写一行到 `runs/<job_id>.jsonl`，字段至少包含：

- `job_id`
- `started_at` / `finished_at`
- `status`（ok/error/skipped）
- `error`（如有）
- `delivered`（是否已投递到 main）
- `output_preview`（截断）

3) **UI 展示（可选）**：TUI 可在侧栏显示“最近 cron 运行情况/失败次数”

---

## 4. 组件协作：完整链路

典型“主会话提醒”链路（无人交互也可发生）：

1) Cron 到点 → `EnqueueSystemEvent(run_id, text, "cron:<job_id>")`  
2) Cron 调 `RequestHeartbeatNow(run_id, reason="cron:<job_id>")`  
3) Heartbeat runner 在合适时机运行 `runHeartbeatOnce(run_id)`  
4) `runHeartbeatOnce` drain system events → 注入 prompt → 模型输出用户可见提醒  
5) 若输出非 `HEARTBEAT_OK` → 写入 `history.jsonl` → TUI 立即可见 / 邮件可回（如配置）

---

## 5. 与本项目集成的推荐落点（包与文件）

为避免把调度逻辑塞进 `tui.go`（过大且难测），建议新增一组内部包（命名可调整）：

```
internal/autonomy/
  systemevents/      # enqueue/peek/drain + format
  heartbeat/         # interval + wake coalescer + runHeartbeatOnce 编排
  cron/              # store + timer + execution + run log
```

并在以下位置挂载：

- 启动：`cmd/agent/main.go` 的 `newAgentRuntime(...)` 或 `runMaster/runChat`
  - 与 auto_cleanup 同级启动/关闭（同一个 ctx）
- Prompt 注入：`internal/agent/tui.go` 的 `runTUITurnStreaming(...)`
  - 在构造 `reqMessages` 时调用 `systemevents.Drain(runID)` 并 append 一个 system message
- 执行 turn：复用 `runTUITurnStreaming(...)`（需要把它抽为可复用的导出函数或放到 agent 包可复用位置）
- Busy/重入保护：复用 TUI 的 `isRunBusy(runID)` 语义；非 TUI 模式用独立的 per-run mutex

> 如果第一版只打算支持 TUI：Heartbeat/Cron 可以只在 TUI 里启动，执行也直接复用现有 `runTurn`/`startRunTaskContext` 模式；之后再抽象成 UI 无关的 executor。

---

## 6. 配置设计（建议新增 `autonomy` 顶层）

示例（加入 `config.exm.json`，仅供设计参考）：

```json
{
  "autonomy": {
    "enabled": true,
    "heartbeat": {
      "enabled": true,
      "every": "30m",
      "coalesce_ms": 250,
      "retry_ms": 1000,
      "path": "HEARTBEAT.md",
      "ok_token": "HEARTBEAT_OK",
      "active_hours": {
        "timezone": "Local",
        "start": "08:00",
        "end": "22:00"
      },
      "dedupe_hours": 24
    },
    "cron": {
      "enabled": true,
      "store_path": "",
      "default_timezone": "UTC",
      "max_timer_delay": "60s",
      "default_timeout": "10m",
      "stuck_run": "2h",
      "min_refire_gap": "2s"
    }
  }
}
```

注意：

- 这里的 `heartbeat.every` 与现有 `start_params.*.heartbeat`（集群心跳）不同名且不同语义
- duration 字段统一用 `time.ParseDuration` 支持格式

---

## 7. CLI/TUI 交互（设计建议）

### 7.1 Heartbeat

- `xinghebot system heartbeat enable|disable|run-now|status`
- TUI：增加一个状态行：`Autonomy: on/off | Heartbeat: 30m | Pending events: N`

### 7.2 Cron

- `xinghebot cron add ...`（支持 `--cron "0 9 * * *"` / `--every 30m` / `--at 2026-02-24T09:00:00Z`）
- `xinghebot cron list|show|remove|enable|disable|run-now`
- TUI：展示最近 10 次 cron run log（失败高亮）

---

## 8. 鲁棒性清单（实现验收标准）

1) **时间跳变/系统挂起**：timer clamp + reload + due 处理，恢复后能补跑 due jobs  
2) **死循环保护**：one-shot at 触发后立即 disable；cron 的 min_refire_gap  
3) **卡死保护**：job 总超时；running 标记 stuck 清理；错误指数退避  
4) **并发安全**：store + ui_state 更新有 lock；同 run_id heartbeat 不并发  
5) **噪声控制**：coalesce；HEARTBEAT_OK 不落 history；24h 内相同提醒去重  
6) **I/O 安全**：store/run log 原子写；路径按 workspace/project_key 隔离；拒绝 symlink（建议复用 memory 的策略）

---

## 9. 交叉编译约束（硬性要求）

实现时必须满足：

- 仅使用标准库 + 纯 Go 依赖（无 CGO）
- 不依赖系统 cron/systemd/launchd 才能工作（内置 timer）
- 文件锁使用 `O_EXCL` lock 文件（本仓库已有实现，跨平台）

---

## 10. 测试策略（建议）

1) **unit**：
- schedule 计算（cron/every/at）
- backoff 与 min_refire_gap
- coalesce / busy retry
- systemevents drain/peek/dedupe/cap

2) **integration（可选）**：
- 用可注入时钟（或短周期）跑一组 jobs，验证日志与投递行为
- 验证 HEARTBEAT_OK 不写 history

---

## 11. 后续扩展（不影响第一版落地）

- 支持更多 DeliveryTarget：email 主动发送、webhook、写入 outbox 文件
- System events 最小持久化（防极端崩溃丢消息）
- “last active run” 路由：把 TUI 当前 run_id 写入一个全局 registry，cron 可 target=last
- 更强的 prompt 结构化（例如把 cron 事件变成严格字段，减少模型理解误差）

