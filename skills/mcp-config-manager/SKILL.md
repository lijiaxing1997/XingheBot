---
name: mcp-config-manager
description: 管理MCP配置文件的skill，支持备份、恢复、启用/禁用MCP服务器，以及查看和修改config.json和mcp.json配置文件。当用户需要管理MCP服务器配置、备份或恢复配置文件、启用或禁用特定MCP功能时使用此skill。
---

# MCP配置管理器

管理MCP（Model Context Protocol）配置文件的skill，支持备份、恢复、启用/禁用MCP服务器，以及查看和修改config.json和mcp.json配置文件。

## 快速开始

### 查看当前配置

```bash
# 查看所有配置信息
python scripts/list_mcps.py

# 只查看config.json
python scripts/list_mcps.py config

# 只查看mcp.json
python scripts/list_mcps.py mcp

# 查看示例配置
python scripts/list_mcps.py example

# 检查MCP服务器状态
python scripts/list_mcps.py status
```

### 备份配置

```bash
# 创建备份
python scripts/backup_config.py

# 列出所有备份
python scripts/backup_config.py list
```

### 管理MCP服务器

```bash
# 列出所有MCP服务器
python scripts/toggle_mcp.py list

# 启用MCP服务器
python scripts/toggle_mcp.py enable calculator

# 禁用MCP服务器
python scripts/toggle_mcp.py disable calculator

# 添加新的MCP服务器
python scripts/toggle_mcp.py add '{"name": "new-server", "transport": "command", "command": "./bin/new-server"}'
```

### 恢复配置

```bash
# 列出所有备份
python scripts/restore_config.py list

# 恢复指定备份
python scripts/restore_config.py restore 1

# 恢复特定文件
python scripts/restore_config.py file config.json
```

## 详细说明

### 配置文件结构

- **config.json**: DeepSeek API配置（API密钥、模型等）
- **mcp.json**: 当前MCP服务器配置
- **mcp.exm.json**: 示例MCP配置

### 脚本功能

#### 1. backup_config.py
备份配置文件到`backup`目录，包含时间戳和备份信息。

#### 2. toggle_mcp.py
管理MCP服务器：列出、启用、禁用、添加服务器。

#### 3. list_mcps.py
查看配置信息和MCP服务器状态。

#### 4. restore_config.py
从备份恢复配置文件，支持完全恢复和部分恢复。

### 参考文档

- [配置文件结构说明](references/config_schema.md)
- [MCP操作指南](references/mcp_operations.md)

### 示例配置

- [示例config.json](assets/example_config.json)
- [示例mcp.json](assets/example_mcp_config.json)

## 使用场景

1. **配置备份与恢复**: 在修改配置前进行备份，出现问题时可快速恢复
2. **MCP服务器管理**: 启用、禁用或添加新的MCP服务器
3. **配置查看与验证**: 查看当前配置状态，验证配置是否正确
4. **环境迁移**: 将配置迁移到新环境

## 注意事项

1. **API密钥安全**: config.json中的API密钥是敏感信息，不应公开分享
2. **备份管理**: 定期清理旧的备份文件，避免占用过多磁盘空间
3. **配置验证**: 修改配置后使用`list_mcps.py status`验证配置
4. **权限要求**: 确保对配置文件和备份目录有读写权限

## 故障排除

### 常见问题

1. **文件不存在错误**: 确保在项目根目录运行脚本
2. **JSON格式错误**: 使用`python -m json.tool`验证JSON格式
3. **权限错误**: 检查文件权限和磁盘空间
4. **路径错误**: 验证mcp.json中的命令路径是否正确

### 获取帮助

查看详细文档：
- `references/config_schema.md` - 配置文件结构说明
- `references/mcp_operations.md` - 完整操作指南
