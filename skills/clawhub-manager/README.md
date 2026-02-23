# ClawHub 技能管理器

## 简介

ClawHub 技能管理器是一个用于管理 ClawHub 技能市场的 DeepSeek 技能。ClawHub 是一个技能市场平台，用户可以在上面发现、安装和使用各种 AI 技能。这个技能提供了完整的 ClawHub CLI 命令参考、使用示例和自动化脚本。

## 功能特性

- 🔍 **技能搜索**：搜索 ClawHub 技能市场上的技能
- 📦 **技能安装**：安装、更新、卸载技能
- 📋 **技能列表**：查看已安装的技能列表
- ⚙️ **技能配置**：配置技能设置和 API Key
- 👤 **账户管理**：管理 ClawHub 账户（登录、登出、查看状态）
- 🛠️ **自动化脚本**：提供测试和验证脚本
- 📚 **完整文档**：包含命令参考和使用示例

## 快速开始

### 1. 安装技能

```bash
# 如果技能尚未安装，可以使用 skill-installer 安装
# 或者直接复制到 skills/ 目录
```

### 2. 使用技能

当用户提到以下内容时，技能会自动触发：
- "搜索 ClawHub 技能"
- "安装 ClawHub 技能"
- "更新技能"
- "查看已安装技能"
- "配置技能 API Key"
- "登录 ClawHub 账户"

### 3. 运行测试

```bash
# 运行测试脚本验证功能
./scripts/test_clawhub.sh

# 运行完整验证
./scripts/validate_skill.py
```

## 目录结构

```
clawhub-manager/
├── SKILL.md                    # 技能主文档
├── references/                 # 参考文档
│   ├── command_reference.md    # 完整命令参考
│   └── usage_examples.md       # 使用示例
└── scripts/                    # 实用脚本
    ├── test_clawhub.sh         # 功能测试脚本
    └── validate_skill.py       # 技能验证脚本
```

## 核心命令示例

### 搜索技能
```bash
clawhub search "文档处理"
clawhub search --popular
clawhub search --category "ai-assistant"
```

### 安装技能
```bash
clawhub install doc-processor
clawhub install skill-name@1.0.0
clawhub install github.com/username/repo
```

### 管理技能
```bash
# 查看已安装技能
clawhub list
clawhub list --verbose

# 更新技能
clawhub update --check
clawhub update --all

# 卸载技能
clawhub uninstall old-skill
```

### 配置技能
```bash
# 查看配置
clawhub config skill-name

# 设置配置
clawhub config skill-name --set api_key=your-key

# 导出/导入配置
clawhub config export > backup.json
clawhub config import < backup.json
```

## 使用场景

### 场景 1：发现新技能
1. 搜索相关技能：`clawhub search "数据分析"`
2. 查看技能详情：`clawhub info data-analyzer`
3. 安装技能：`clawhub install data-analyzer`
4. 配置技能：`clawhub config data-analyzer --set api_key=xxx`

### 场景 2：技能维护
1. 检查更新：`clawhub update --check`
2. 更新所有技能：`clawhub update --all`
3. 清理不需要的技能：`clawhub uninstall deprecated-skill`

### 场景 3：团队协作
1. 导出配置：`clawhub config export > team-config.json`
2. 分享配置文件
3. 导入配置：`clawhub config import < team-config.json`

## 故障排除

### 常见问题

1. **安装失败**
   - 检查网络连接
   - 更新 ClawHub CLI：`npm update -g @clawhub/cli`
   - 使用调试模式：`clawhub --debug install skill-name`

2. **登录问题**
   - 清除缓存：`clawhub logout && clawhub login`
   - 检查账户凭据
   - 使用环境变量：`CLAWHUB_API_KEY=your-key`

3. **技能不工作**
   - 检查配置：`clawhub config skill-name`
   - 查看文档：`clawhub docs skill-name`
   - 重新安装：`clawhub uninstall skill-name && clawhub install skill-name`

### 调试技巧

```bash
# 启用调试模式
clawhub --debug command

# 查看详细日志
CLAWHUB_LOG_LEVEL=debug clawhub command

# 清除缓存
rm -rf ~/.clawhub/cache/*
```

## 最佳实践

1. **定期更新**：每月运行 `clawhub update --check` 检查更新
2. **备份配置**：定期导出配置备份
3. **测试环境**：在生产环境使用前充分测试
4. **权限管理**：设置最小必要权限
5. **版本控制**：将技能配置加入版本控制

## 自动化脚本

技能包含以下实用脚本：

### test_clawhub.sh
- 测试 ClawHub CLI 基本功能
- 验证命令语法
- 提供使用示例

### validate_skill.py
- 验证技能完整性
- 检查所有文件是否存在
- 运行功能测试

## 相关资源

- [ClawHub 官方网站](https://clawhub.com)
- [ClawHub CLI 文档](https://docs.clawhub.com/cli)
- [技能开发指南](https://docs.clawhub.com/develop)
- [社区论坛](https://community.clawhub.com)

## 许可证

此技能遵循 MIT 许可证。

## 贡献

欢迎提交 Issue 和 Pull Request 来改进这个技能。

---

**提示**：使用此技能前，请确保已安装 ClawHub CLI 并配置好账户。