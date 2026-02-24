# Skills 使用说明

本项目的 skills 采用 “`skills/<skill-name>/SKILL.md`” 的目录约定，用于给模型提供**可选择加载**的本地工作流说明（类似 OpenClaw 的 skills）。

## 1) skills 保存在哪里？

默认目录是：

- `<git repo root>/skills`

你也可以通过以下方式改默认目录：

- 环境变量：`SKILLS_DIR=/abs/or/relative/path`
- CLI 参数：`xinghebot chat --skills-dir <path>` / `xinghebot worker --skills-dir <path>`

为避免“目录乱建”，进程启动时会把 `--skills-dir` 解析为**绝对路径**，并在主/子 Agent 之间传递同一个绝对路径。

## 2) skill_create 为什么以前会“乱建”？

常见原因：

- `--skills-dir` 是相对路径 + 子 Agent 的工作目录（`work_dir`）不同，导致相对路径落点不同。
- tool schema 允许传 `dir`，模型偶发把 skill 建在不期望的目录（比如项目根目录、运行目录等）。

当前已优化：

- `skill_create/skill_install/skill_load/skill_list` 不再暴露 `dir` 参数（始终使用配置的 skills 根目录）。
- skill 名称包含中文/特殊字符时，目录名会自动生成稳定的 `skill-<hash>` 形式，避免全部落到同一个 `skills/skill` 目录。

## 3) 目录结构约定

最小结构：

```
skills/<skill-name>/
  SKILL.md
```

推荐结构（可选）：

```
skills/<skill-name>/
  SKILL.md
  scripts/
  references/
  assets/
  agents/openai.yaml
```

## 4) 触发与加载（运行时行为）

- **子 Agent（worker）** 的 system prompt 会包含 `<available_skills> ... </available_skills>` 列表（name/description/location），并由子 Agent 决定是否需要加载 skill。
- **主 Agent（chat/dispatcher）** 的 system prompt 也会包含一份 `<available_skills>` 列表 + 子 Agent 工具能力简介（skills/MCP/内置工具），用于更好地指导子 Agent（生成更准确的 agent_spawn 任务与 agent_control(message) 提示）。但主 Agent 本身仍应保持“只调度不执行”：在 dispatcher 模式下不会直接调用 skill_* / MCP / 文件 / exec 等执行工具。
- 子 Agent 应先扫描 `<description>` 判断是否需要 skill：
  - 若只有一个 skill 明显匹配：用 `skill_load(name)` 加载其 SKILL.md 并遵循。
  - 若多个可能匹配：选最具体的一个再加载。
  - 若不匹配：不要加载任何 SKILL.md。

## 5) 常用命令

- 列出 skills：`xinghebot skills list`
- 创建 skill：`xinghebot skills create --name <name> --description <desc>`
- 安装 skill（本地）：`xinghebot skills install --local <dir>`
- 安装 skill（GitHub）：`xinghebot skills install --repo owner/repo --path path/in/repo --ref main`
