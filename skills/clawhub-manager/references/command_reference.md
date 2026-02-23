# ClawHub CLI 命令参考

## 命令概览

### 账户管理
- `clawhub login` - 登录 ClawHub 账户
- `clawhub logout` - 登出账户
- `clawhub whoami` - 查看当前登录用户
- `clawhub register` - 注册新账户

### 技能搜索和发现
- `clawhub search [query]` - 搜索技能
- `clawhub search --popular` - 搜索热门技能
- `clawhub search --recent` - 搜索最新技能
- `clawhub search --category <category>` - 按类别搜索
- `clawhub search --tag <tag>` - 按标签搜索
- `clawhub search --author <author>` - 按作者搜索

### 技能安装和管理
- `clawhub install <skill>` - 安装技能
- `clawhub install <skill>@<version>` - 安装特定版本
- `clawhub install <github-repo>` - 从 GitHub 安装
- `clawhub uninstall <skill>` - 卸载技能
- `clawhub uninstall --force` - 强制卸载（不提示）
- `clawhub list` - 列出已安装技能
- `clawhub list --verbose` - 显示详细信息
- `clawhub list --category <category>` - 按类别列出

### 技能更新
- `clawhub update --check` - 检查可用更新
- `clawhub update --all` - 更新所有技能
- `clawhub update <skill>` - 更新特定技能
- `clawhub update --force` - 强制更新（忽略兼容性检查）

### 技能配置
- `clawhub config <skill>` - 查看技能配置
- `clawhub config <skill> --set <key>=<value>` - 设置配置项
- `clawhub config <skill> --unset <key>` - 删除配置项
- `clawhub config export` - 导出所有配置
- `clawhub config import` - 导入配置

### 技能信息
- `clawhub info <skill>` - 查看技能详情
- `clawhub docs <skill>` - 查看技能文档
- `clawhub versions <skill>` - 查看版本历史
- `clawhub stats <skill>` - 查看使用统计

### 其他命令
- `clawhub --version` - 显示 CLI 版本
- `clawhub --help` - 显示帮助信息
- `clawhub --debug` - 启用调试模式
- `clawhub --verbose` - 显示详细输出

## 详细命令说明

### clawhub search

搜索 ClawHub 技能市场中的技能。

**语法：**
```bash
clawhub search [options] [query]
```

**选项：**
- `--popular` - 显示热门技能
- `--recent` - 显示最近添加的技能
- `--category <category>` - 按类别筛选
- `--tag <tag>` - 按标签筛选
- `--author <author>` - 按作者筛选
- `--limit <number>` - 限制结果数量（默认：20）
- `--format <format>` - 输出格式（json, table, list）

**示例：**
```bash
# 搜索文档处理相关技能
clawhub search "文档处理"

# 搜索热门 AI 助手技能
clawhub search --popular --category "ai-assistant"

# 以 JSON 格式输出
clawhub search "数据分析" --format json
```

### clawhub install

安装技能到本地环境。

**语法：**
```bash
clawhub install <skill-spec> [options]
```

**技能规格：**
- `<skill-name>` - 技能名称（从 ClawHub 市场）
- `<skill-name>@<version>` - 特定版本
- `<github-repo>` - GitHub 仓库（如：github.com/username/repo）

**选项：**
- `--force` - 强制安装（覆盖现有版本）
- `--no-deps` - 不安装依赖
- `--save` - 保存到配置文件
- `--global` - 全局安装（所有用户可用）

**示例：**
```bash
# 安装最新版本
clawhub install doc-processor

# 安装特定版本
clawhub install doc-processor@1.2.0

# 从 GitHub 安装
clawhub install github.com/clawhub/awesome-skill

# 强制安装（覆盖）
clawhub install skill-name --force
```

### clawhub config

管理技能配置。

**语法：**
```bash
clawhub config <skill> [options]
```

**子命令：**
- `clawhub config <skill>` - 查看配置
- `clawhub config <skill> --set <key>=<value>` - 设置配置
- `clawhub config <skill> --unset <key>` - 删除配置
- `clawhub config export` - 导出所有配置
- `clawhub config import` - 导入配置

**示例：**
```bash
# 查看配置
clawhub config openai-assistant

# 设置 API Key
clawhub config openai-assistant --set api_key=sk-xxx

# 设置多个配置
clawhub config weather-skill --set api_key=xxx --set units=metric

# 删除配置
clawhub config openai-assistant --unset api_key

# 导出配置到文件
clawhub config export > my-config.json

# 从文件导入配置
clawhub config import < my-config.json
```

### clawhub update

更新已安装的技能。

**语法：**
```bash
clawhub update [options] [skill...]
```

**选项：**
- `--check` - 只检查更新，不实际更新
- `--all` - 更新所有技能
- `--force` - 强制更新（忽略兼容性检查）
- `--dry-run` - 模拟更新，显示将要更新的内容

**示例：**
```bash
# 检查所有技能的更新
clawhub update --check

# 更新所有技能
clawhub update --all

# 更新特定技能
clawhub update doc-processor image-editor

# 强制更新（可能破坏兼容性）
clawhub update --all --force
```

## 配置示例

### API Key 配置
```bash
# 设置 OpenAI API Key
clawhub config openai-skill --set api_key=sk-xxx

# 设置天气 API Key
clawhub config weather-skill --set api_key=xxx --set units=metric

# 设置翻译服务配置
clawhub config translator --set provider=deepl --set api_key=xxx --set target_lang=zh
```

### 技能特定配置
```bash
# 文档处理技能
clawhub config doc-processor --set \
  max_file_size=10MB \
  supported_formats=pdf,docx,txt \
  output_dir=./processed

# 图像处理技能
clawhub config image-editor --set \
  quality=90 \
  resize_mode=fit \
  watermark_enabled=true \
  watermark_text="Processed by ClawHub"
```

## 环境变量

ClawHub CLI 支持以下环境变量：

- `CLAWHUB_API_KEY` - ClawHub API Key（替代登录）
- `CLAWHUB_API_URL` - ClawHub API 端点（用于自托管）
- `CLAWHUB_CONFIG_DIR` - 配置目录路径
- `CLAWHUB_CACHE_DIR` - 缓存目录路径
- `CLAWHUB_LOG_LEVEL` - 日志级别（debug, info, warn, error）

## 配置文件位置

- **全局配置**: `~/.clawhub/config.json`
- **项目配置**: `./.clawhub/config.json`
- **技能配置**: `~/.clawhub/skills/<skill-name>/config.json`

## 故障排除

### 常见错误

1. **认证失败**
   ```
   Error: Authentication failed
   ```
   **解决方案**: 运行 `clawhub logout && clawhub login`

2. **网络连接问题**
   ```
   Error: Cannot connect to ClawHub server
   ```
   **解决方案**: 检查网络连接，或设置 `CLAWHUB_API_URL`

3. **技能安装失败**
   ```
   Error: Failed to install skill
   ```
   **解决方案**: 检查技能名称是否正确，或使用 `--debug` 模式查看详细错误

### 调试技巧

```bash
# 启用调试模式
clawhub --debug search "test"

# 查看详细日志
CLAWHUB_LOG_LEVEL=debug clawhub install skill-name

# 清除缓存
rm -rf ~/.clawhub/cache/*
```

## 最佳实践

1. **使用版本控制**: 在团队项目中，将 `.clawhub/config.json` 加入版本控制
2. **定期更新**: 每月运行 `clawhub update --check` 检查更新
3. **备份配置**: 定期导出配置备份：`clawhub config export > backup-$(date +%Y%m%d).json`
4. **测试环境**: 在生产环境使用前，在测试环境验证技能更新
5. **权限管理**: 根据需要设置技能的最小必要权限