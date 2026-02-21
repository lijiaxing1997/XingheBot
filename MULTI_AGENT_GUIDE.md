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
