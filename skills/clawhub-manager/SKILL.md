---
name: clawhub-manager
description: 用于管理 ClawHub 技能市场的技能，支持搜索、安装、更新、卸载技能等操作。当用户需要管理 ClawHub 技能时使用此技能，包括：搜索 ClawHub 上的技能、安装/更新/卸载技能、查看已安装技能列表、配置技能（如设置 API Key）、管理 ClawHub 账户（登录、登出、查看状态）。
---

# ClawHub 技能管理器

## 概述

ClawHub 技能管理器是一个用于管理 ClawHub 技能市场的技能，支持搜索、安装、更新、卸载技能等操作。ClawHub 是一个技能市场平台，用户可以在上面发现、安装和使用各种 AI 技能。

## 前置要求

在使用此技能之前，请确保：

1. **已安装 ClawHub CLI**：需要安装 ClawHub 命令行工具
2. **已配置 ClawHub 账户**：需要登录 ClawHub 账户才能访问技能市场
3. **网络连接**：需要能够访问 ClawHub 服务器

## 可用命令和示例

### 1. 搜索技能

搜索 ClawHub 技能市场上的技能：

```bash
# 搜索特定技能
clawhub search "技能名称"

# 搜索特定类别的技能
clawhub search --category "category-name"

# 搜索热门技能
clawhub search --popular

# 搜索最新技能
clawhub search --recent
```

### 2. 安装技能

从 ClawHub 安装技能：

```bash
# 安装特定技能
clawhub install skill-name

# 安装特定版本的技能
clawhub install skill-name@1.0.0

# 从 GitHub 仓库安装技能
clawhub install github.com/username/repo
```

### 3. 更新技能

更新已安装的技能：

```bash
# 更新所有技能
clawhub update --all

# 更新特定技能
clawhub update skill-name

# 检查可用的更新
clawhub update --check
```

### 4. 卸载技能

卸载不再需要的技能：

```bash
# 卸载特定技能
clawhub uninstall skill-name

# 卸载多个技能
clawhub uninstall skill1 skill2 skill3

# 强制卸载（不提示确认）
clawhub uninstall skill-name --force
```

### 5. 查看已安装技能

列出已安装的技能：

```bash
# 列出所有已安装技能
clawhub list

# 列出详细信息
clawhub list --verbose

# 列出特定类别的技能
clawhub list --category "category-name"
```

### 6. 配置技能

配置技能的设置和 API Key：

```bash
# 查看技能配置
clawhub config skill-name

# 设置技能配置
clawhub config skill-name --set key=value

# 设置 API Key
clawhub config skill-name --set api_key=your-api-key

# 删除配置项
clawhub config skill-name --unset key
```

### 7. 账户管理

管理 ClawHub 账户：

```bash
# 登录 ClawHub 账户
clawhub login

# 登出账户
clawhub logout

# 查看账户状态
clawhub whoami

# 注册新账户
clawhub register
```

### 8. 技能信息

查看技能的详细信息：

```bash
# 查看技能详情
clawhub info skill-name

# 查看技能文档
clawhub docs skill-name

# 查看技能版本历史
clawhub versions skill-name
```

## 使用场景

### 场景 1：发现和安装新技能

当用户想要扩展 AI 能力时，可以使用此技能搜索和安装新的技能：

1. 搜索相关技能：`clawhub search "文档处理"`
2. 查看技能详情：`clawhub info doc-processor`
3. 安装技能：`clawhub install doc-processor`
4. 配置技能：`clawhub config doc-processor --set api_key=your-key`

### 场景 2：管理已安装技能

当用户需要维护已安装的技能时：

1. 查看已安装技能：`clawhub list`
2. 检查更新：`clawhub update --check`
3. 更新技能：`clawhub update skill-name`
4. 卸载不需要的技能：`clawhub uninstall old-skill`

### 场景 3：团队协作

当团队需要共享技能配置时：

1. 导出技能配置：`clawhub config export > skills-config.json`
2. 分享配置文件
3. 导入配置：`clawhub config import < skills-config.json`

## 故障排除

### 常见问题

#### 1. 安装失败

**问题**：`clawhub install` 命令失败
**解决方案**：
- 检查网络连接：`ping clawhub.com`
- 检查 ClawHub CLI 版本：`clawhub --version`
- 更新 CLI：`npm update -g @clawhub/cli`

#### 2. 登录问题

**问题**：无法登录 ClawHub 账户
**解决方案**：
- 检查账户凭据
- 重置密码：访问 ClawHub 网站
- 清除缓存：`clawhub logout && clawhub login`

#### 3. 技能不工作

**问题**：安装的技能无法正常工作
**解决方案**：
- 检查技能配置：`clawhub config skill-name`
- 查看技能文档：`clawhub docs skill-name`
- 重新安装技能：`clawhub uninstall skill-name && clawhub install skill-name`

### 调试模式

启用调试模式获取更多信息：

```bash
# 启用调试输出
clawhub --debug command

# 查看详细日志
clawhub --verbose command
```

## 最佳实践

1. **定期更新**：定期运行 `clawhub update --check` 检查技能更新
2. **备份配置**：定期导出技能配置备份
3. **测试环境**：在生产环境使用前，先在测试环境验证技能
4. **权限管理**：根据需要设置适当的技能权限

## 参考文档

如需查看完整的命令参考和详细说明，请参阅：[命令参考](references/command_reference.md)

## 使用示例

更多实际使用场景和示例代码，请参阅：[使用示例](references/usage_examples.md)

## 测试脚本

技能包含一个测试脚本，可用于验证 ClawHub CLI 的基本功能：

```bash
# 运行测试脚本
./scripts/test_clawhub.sh
```

## 相关资源

- [ClawHub 官方网站](https://clawhub.com)
- [ClawHub CLI 文档](https://docs.clawhub.com/cli)
- [技能开发指南](https://docs.clawhub.com/develop)
- [社区论坛](https://community.clawhub.com)

## 注意事项

1. 某些技能可能需要额外的依赖或环境配置
2. 技能更新可能会引入不兼容的更改
3. 建议在生产环境使用前充分测试
4. 关注技能的安全性和权限设置
