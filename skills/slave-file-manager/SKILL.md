---
name: slave-file-manager
description: >
  Manage files on cluster slaves reliably: locate files under the slave's cluster.files.root_dir (default .cluster/files),
  transfer original files between master and slave via remote_file_get/remote_file_put, and prepare correct email attachment
  directives/paths (relative to .cluster/files; never prefix .cluster/files). Use when users ask to download/upload/export
  files from/to a slave, request the original file, or emails fail due to wrong attachment paths.
---

# Slave File Manager（Slave 文件管理）

## Overview

用于“Slave 文件管理/取回/上传/发附件”的稳定工作流：当用户要 Slave 上的文件时，默认返回**原文件**（通过文件传输工具），并生成**不会写错**的附件路径。

## Core rules（必须遵守）

1) **用户要文件 = 要原文件**  
除非用户明确说“只要内容/摘要”，否则不要让 Slave 去 `read_file` 然后把内容贴回来；用文件传输工具把文件取回到 Master。

2) **文件内容走传输，不走上下文**  
用 `remote_file_get` / `remote_file_put` 传文件；`remote_agent_run` 只用于“找路径/列目录/打包”，不要用于“读取文件内容并分析后返回”。

3) **附件路径永远写相对路径（避免重复拼 root_dir）**  
附件路径必须是相对 `cluster.files.root_dir`（默认 `.cluster/files`）的路径，**不要**写 `.cluster/files/` 前缀。

## Path rules（最容易出错的点）

### 1) Email attachments（Master 侧）

- 附件指令写在**最终回复**里，例如：
  - `ATTACH: outbox/report.txt`
  - `附件: outbox/report.txt`
- 附件路径必须满足：
  - 在 `cluster.files.root_dir`（默认 `.cluster/files`）之下
  - **路径写法是相对 root_dir 的相对路径**（推荐 `outbox/...` 或 `inbox/...`）
  - **不要写** `.cluster/files/` 前缀

✅ 正确：
- `附件: outbox/windows-memory-analysis.md`
- `ATTACH: inbox/slave-01/2026-02-24/xfer-xxx__windows-memory-analysis.md`

❌ 错误（会被拼成 `.cluster/files/.cluster/files/...`）：
- `附件: .cluster/files/outbox/windows-memory-analysis.md`

如果文件不在 `.cluster/files/` 下：先把文件 `copy_file` 到 `.cluster/files/outbox/`，再用 `outbox/...` 作为附件路径。

### 2) remote_file_get.remote_path（Slave 侧）

`remote_file_get.remote_path` 必须是 **相对 Slave 的** `cluster.files.root_dir`（通常就是 `.cluster/files`）的相对路径。

✅ 正确：
- `outbox/windows-memory-analysis.md`
- `inbox/master/2026-02-24/xfer-xxx__input.zip`

❌ 错误：
- `.cluster/files/outbox/windows-memory-analysis.md`
- `/root/xinghebot/.cluster/files/outbox/windows-memory-analysis.md`
- `C:\\opt\\xinghebot\\.cluster\\files\\outbox\\windows-memory-analysis.md`

## Workflows

### A) 取回 Slave 文件并交付给用户（默认返回原文件）

1) 明确这几个信息（缺就问/补）：
   - `slave_id`
   - 文件在 Slave 上的相对路径（相对 `.cluster/files`）：例如 `outbox/...` / `inbox/...`

2) 如果用户只说“把报告/日志发我”，但不知道路径：
   - 用 `remote_agent_run` 去 Slave 上**列目录/搜索文件名**（例如列 `.cluster/files/outbox/`），只返回路径列表
   - 不要在 Slave 上把文件内容读出来贴回

3) 用文件传输工具取回：
   - `remote_file_get(slave=<slave_id>, remote_path=<relative path>)`
   - 文件会落到 Master 的 `.cluster/files/inbox/<slave_id>/<YYYY-MM-DD>/...`

4) 需要发邮件附件时（强烈推荐）：
   - 把 Master inbox 里的文件 `copy_file` 到 `.cluster/files/outbox/<友好文件名>`
   - 最终回复写：`附件: outbox/<友好文件名>`（不要带 `.cluster/files/`）

5) 回复里至少给出：
   - Slave: `<slave_id>`
   - Slave path: `<remote_path>`（相对 `.cluster/files`）
   - Master saved path: `<local_path>`（或相对 `inbox/...`）
   - Attachment line（仅邮件）：`附件: <outbox/... 或 inbox/...>`（相对路径）

### B) 把本地文件传到 Slave

1) 确认 Master 本地文件路径 `local_path`（必要时先整理到 `.cluster/files/outbox/` 便于审计）
2) 调用：
   - `remote_file_put(slave=<slave_id>, local_path=<path>, remote_name=<可选>)`
3) 注意：
   - 文件会保存在 Slave 的 `.cluster/files/inbox/...` 下（由接收方决定落盘路径）
   - 若必须移动到别的目录：再用 `remote_agent_run` 去执行移动（先传文件，再移动）

## Quick sanity checklist（防止“又发错路径”）

- 附件路径：只写 `outbox/...` 或 `inbox/...`，**绝不写** `.cluster/files/...`
- 从 Slave 拿文件：用 `remote_file_get`，`remote_path` 只写相对路径（例如 `outbox/...`）
- 用户要文件：默认给原文件；需要分析也先取回到 Master 再分析
