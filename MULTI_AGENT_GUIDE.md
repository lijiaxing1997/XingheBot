# Multi-Agent 架构使用说明

本项目已支持“主 Agent + 子 Agent（多进程）”协作。

## 1. 目标能力

- 主 Agent 可以创建 run，并按任务创建多个子 Agent。
- 子 Agent 独立进程并行执行。
- 进程间通过 JSON 文件通信（state/commands/events/signals/result）。
- 支持阻塞等待、暂停/恢复、取消等控制。
- 子 Agent 与主 Agent 一样，拥有完整工具权限（文件/exec/skill/MCP/多 Agent 工具）。

## 2. 目录结构

默认根目录：`.multi_agent/runs`

每个 run:

- `run.json`
- `ui_state.json`（TUI 归档隐藏的子 Agent 可见性配置）
- `agents/<agent_id>/spec.json`
- `agents/<agent_id>/state.json`
- `agents/<agent_id>/commands.jsonl`
- `agents/<agent_id>/events.jsonl`
- `agents/<agent_id>/result.json`
- `agents/<agent_id>/stdout.log`
- `agents/<agent_id>/stderr.log`
- `signals/<key>.jsonl`

## 3. 关键工具

- `agent_run_create`: 创建/获取 run。
- `agent_run_list`: 列出当前 run（用于不知道 run_id 时回答“有什么正在执行”）。
- `agent_run_prune`: 清理历史 run（默认跳过 active；默认保留 failed；支持 `archive|delete`，支持 `dry_run` 预览）。
- `agent_spawn`: 启动子 Agent 进程。
- `agent_state`: 查询子 Agent 状态。
- `agent_progress`: 非阻塞进度快照（state + 最近 events；单个 agent 还会带 stdout/stderr tail）。用于“快速看进度”，避免循环 poll `agent_events`。
- `agent_wait`: 阻塞等待一个或多个子 Agent 结束。
- `agent_control`: 发送 `pause|resume|cancel|message` 命令。
- `agent_events`: 拉取事件流。
- `agent_inspect`: 装载子 Agent 执行上下文（spec/state、最近 events/commands、stdout/stderr tail），便于排查与指导；默认不包含完整 `spec.task`（需要时传 `include_task=true`）。
- `agent_result`: 读取最终结果。
- `agent_signal_send`: 发送信号（跨 Agent 协调）。
- `agent_signal_wait`: 等待信号（阻塞）。
- `subagents`: 一体化子 Agent 编排工具：`list`/`steer`/`kill`（用于快速查看、引导、取消/强杀子 Agent）。

## 4. 典型流程

1. 主 Agent 调 `agent_run_create` 获得 `run_id`。  
2. 主 Agent 调多次 `agent_spawn` 并行创建子 Agent。  
3. 用 `agent_wait` 阻塞等待全部子 Agent 完成。  
4. 用 `agent_result` 汇总结果。  
5. 需要中途干预时，用 `agent_control` 发送 `pause/resume/cancel`。  
   - `message`：`payload` 推荐 `{ "text": "...", "role": "user" }`。子 Agent 会在下一次 model call 前注入为一条消息，从而实现“主 Agent 指导子 Agent 执行”。
6. 需要“barrier/event”协同时，子 Agent 用 `agent_signal_send` + `agent_signal_wait`。

## 5. CLI 入口

- 交互模式：`agent chat ...`
- 子 Agent worker 进程：`agent worker --run-root ... --run-id ... --agent-id ...`

`agent chat` 默认是 dispatcher 模式：主 Agent 只允许调用 `agent_*` / `subagents` / `skill_*` / `mcp_reload`，避免主 Agent“直接干活”。如需允许主 Agent 直接使用文件/exec 工具，可用：`agent chat --chat-tool-mode full`。

`agent_spawn` 会自动拉起 `agent worker`，通常不需要手动执行。

## 6. 清理/归档建议

`.multi_agent/runs` 会随着运行次数增多而变大/变乱，通常不需要长期保留所有 run。

推荐做法：

- 日常：只保留最近 N 个成功 run（例如 `keep_last=20`），把更老的成功 run 归档到 `.multi_agent/archive`。
- 排查问题：默认保留 failed run（`include_failed=false`），避免把排错证据清掉。

示例（先预览，再执行）：

1) 预览（不会改动磁盘）：
   - `agent_run_prune` with `{ "mode": "archive", "keep_last": 20, "older_than_days": 7, "dry_run": true }`
2) 执行归档：
   - `agent_run_prune` with `{ "mode": "archive", "keep_last": 20, "older_than_days": 7, "dry_run": false }`
3) 直接删除（谨慎）：
   - `agent_run_prune` with `{ "mode": "delete", "keep_last": 20, "older_than_days": 7, "dry_run": false }`

## 7. 自动定期清理（类似 OpenClaw 的 archiveAfterMinutes）

`agent chat` 会在后台启动一个清理 sweeper，按配置定期把**已结束**的 run 从 `.multi_agent/runs` 归档到 `.multi_agent/archive`（默认 **archive**，不会直接删除）。

配置位置：`config.json` 的 `multi_agent.cleanup`。

默认值（未配置时）：

```json
{
  "multi_agent": {
    "cleanup": {
      "enabled": true,
      "mode": "archive",
      "interval_minutes": 10,
      "archive_after_minutes": 60,
      "keep_last": 20,
      "include_failed": false,
      "dry_run": false
    }
  }
}
```

说明：

- 只处理**所有子 Agent 都已进入终态**（completed/failed/canceled）的 run；active run 一律跳过。
- `archive_after_minutes` 以 run 的**结束时间（ended_at）**为准（尽量用 agent 的 `finished_at` 推导；没有则回退到 `updated_at/created_at`）。
- 默认 `include_failed=false`：failed run 会留在 `.multi_agent/runs` 便于排查；想自动归档 failed 也可设为 `true`。
- `dry_run=true` 只预览，不改动磁盘。

如果你不想自动清理：

```json
{ "multi_agent": { "cleanup": { "enabled": false } } }
```

## 8. TUI 子 Agent 太多（隐藏但保留）

TUI 右侧 `Status` / `TAB` 只影响“显示”，不必删除子 Agent 文件。

- 默认行为：`completed/canceled` 会被隐藏（`Ctrl+T` 可切换显示/隐藏 finished），`failed` 默认会显示便于排错。
- 需要更灵活的“按 agent_id 归档隐藏 / 再恢复显示”（任何状态都能隐藏，且**只作用于当前 session/run**）：
  - 隐藏（归档）：`agent_subagent_hide` with `{ "agent_id": "agent-xxxx", "reason": "..." }`
  - 列表/搜索：`agent_subagent_list` with `{ "scope": "hidden|visible|all", "query": "..." }`
  - 恢复显示：`agent_subagent_show` with `{ "agent_id": "agent-xxxx" }`

隐藏信息会写入当前 run 的 `.multi_agent/runs/<run_id>/ui_state.json`，不会影响其它 run。
