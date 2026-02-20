# MCP操作指南

## 概述

MCP（Model Context Protocol）配置管理器提供了一套完整的工具来管理MCP服务器的配置。本指南详细介绍了各种操作的使用方法。

## 快速开始

### 1. 查看当前配置

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

### 2. 备份配置

```bash
# 创建备份
python scripts/backup_config.py

# 列出所有备份
python scripts/backup_config.py list
```

### 3. 管理MCP服务器

```bash
# 列出所有MCP服务器
python scripts/toggle_mcp.py list

# 启用MCP服务器（实际上只是确认存在）
python scripts/toggle_mcp.py enable calculator

# 禁用MCP服务器（从配置中移除）
python scripts/toggle_mcp.py disable calculator

# 添加新的MCP服务器
python scripts/toggle_mcp.py add '{"name": "new-server", "transport": "command", "command": "./bin/new-server"}'
```

### 4. 恢复配置

```bash
# 列出所有备份
python scripts/restore_config.py list

# 恢复指定备份（例如恢复第1个备份）
python scripts/restore_config.py restore 1

# 恢复特定文件
python scripts/restore_config.py file config.json

# 从指定时间戳恢复文件
python scripts/restore_config.py file mcp.json 20240101_120000
```

## 详细操作说明

### 备份操作

#### 自动备份
备份脚本会自动：
1. 创建`backup`目录（如果不存在）
2. 备份`config.json`和`mcp.json`文件
3. 为备份文件添加时间戳后缀
4. 创建备份信息文件

#### 备份文件命名规则
```
config.json.backup.20240101_120000
mcp.json.backup.20240101_120000
backup_info.20240101_120000.json
```

#### 备份信息文件内容
```json
{
  "timestamp": "20240101_120000",
  "files": [
    {
      "original": "config.json",
      "backup": "config.json.backup.20240101_120000",
      "size": 1234
    },
    {
      "original": "mcp.json",
      "backup": "mcp.json.backup.20240101_120000",
      "size": 5678
    }
  ],
  "backup_dir": "/path/to/project/backup"
}
```

### MCP服务器管理

#### 列出MCP服务器
显示当前配置的所有MCP服务器的详细信息：
- 服务器名称
- 传输方式
- 命令路径
- 参数列表
- 环境变量

#### 启用MCP服务器
"启用"操作实际上只是验证服务器配置是否存在。要真正启用服务器，需要确保：
1. 服务器配置在`mcp.json`中
2. 服务器可执行文件存在且可执行
3. 所有依赖项已安装

#### 禁用MCP服务器
"禁用"操作会从`mcp.json`配置中移除指定的服务器。这不会删除服务器文件，只是从配置中移除。

#### 添加MCP服务器
添加新服务器需要提供完整的配置JSON。示例配置：

```json
{
  "name": "weather",
  "transport": "command",
  "command": "./bin/weather-mcp",
  "args": ["--api-key", "${WEATHER_API_KEY}"],
  "env": {
    "PYTHONPATH": "${PYTHONPATH}:./mcp/weather"
  }
}
```

### 恢复操作

#### 完全恢复
恢复指定备份中的所有文件。脚本会：
1. 备份当前文件（如果存在）
2. 从备份恢复文件
3. 显示恢复结果

#### 部分恢复
恢复特定文件，可以选择从特定时间戳的备份恢复。

#### 安全措施
恢复操作前会：
1. 显示要恢复的文件列表
2. 要求用户确认
3. 备份当前文件（避免数据丢失）

## 使用场景示例

### 场景1：测试新MCP服务器

```bash
# 1. 备份当前配置
python scripts/backup_config.py

# 2. 添加新服务器
python scripts/toggle_mcp.py add '{"name": "test-server", "transport": "command", "command": "./bin/test"}'

# 3. 测试服务器
# ... 进行测试 ...

# 4. 如果测试失败，恢复配置
python scripts/restore_config.py restore 1
```

### 场景2：临时禁用MCP服务器

```bash
# 1. 查看当前服务器
python scripts/toggle_mcp.py list

# 2. 禁用特定服务器
python scripts/toggle_mcp.py disable calculator

# 3. 进行其他操作
# ... 不需要计算器的操作 ...

# 4. 重新启用（需要重新添加）
python scripts/toggle_mcp.py add '{"name": "calculator", "transport": "command", "command": "./bin/calculator-mcp", "env": {"PYTHONPATH": "${PYTHONPATH}:./mcp/calculator"}}'
```

### 场景3：配置迁移

```bash
# 1. 在新环境备份当前配置（如果有）
python scripts/backup_config.py

# 2. 从旧环境复制备份文件
# 3. 恢复配置
python scripts/restore_config.py restore 1

# 4. 调整路径和环境变量
# 编辑mcp.json中的命令路径和环境变量
```

## 故障排除

### 常见问题

#### Q: 备份时显示"文件不存在"
A: 检查`config.json`和`mcp.json`文件是否在项目根目录

#### Q: 恢复时找不到备份
A: 确认备份目录存在且包含备份文件

#### Q: MCP服务器添加失败
A: 检查JSON格式是否正确，服务器名称是否唯一

#### Q: 配置文件格式错误
A: 使用JSON验证工具检查，或从备份恢复

### 错误处理

1. **权限错误**：确保对配置文件和备份目录有读写权限
2. **磁盘空间不足**：清理旧的备份文件
3. **JSON格式错误**：使用`json.tool`验证格式：`python -m json.tool config.json`
4. **路径错误**：检查命令路径是否正确，使用绝对路径或相对于项目根目录的路径

## 最佳实践

### 配置管理
1. **修改前备份**：每次修改配置前都进行备份
2. **版本控制**：将备份信息文件纳入版本控制
3. **定期清理**：定期清理旧的备份文件
4. **文档记录**：记录重要的配置变更

### MCP服务器管理
1. **唯一命名**：为每个MCP服务器使用唯一的名称
2. **环境变量**：使用环境变量管理敏感信息
3. **路径验证**：添加服务器前验证命令路径
4. **依赖管理**：确保所有依赖项已正确安装

### 恢复策略
1. **测试恢复**：定期测试恢复流程
2. **多版本备份**：保留多个时间点的备份
3. **验证恢复**：恢复后验证配置是否正确
4. **回滚计划**：制定明确的回滚计划

## 高级功能

### 自定义备份目录
```bash
# 创建自定义备份目录
mkdir -p my_backups

# 使用自定义目录备份
python -c "
import sys
sys.path.insert(0, 'scripts')
from backup_config import backup_config_files
backup_config_files('my_backups')
"
```

### 批量操作
```bash
# 批量禁用多个服务器
for server in calculator greeter; do
  python scripts/toggle_mcp.py disable $server
done

# 批量恢复多个文件
for file in config.json mcp.json; do
  python scripts/restore_config.py file $file
done
```

### 自动化脚本
创建自动化脚本简化常用操作：

```bash
#!/bin/bash
# backup_and_update.sh

# 备份当前配置
python scripts/backup_config.py

# 更新MCP服务器
python scripts/toggle_mcp.py disable old-server
python scripts/toggle_mcp.py add '{"name": "new-server", "transport": "command", "command": "./bin/new-server"}'

# 验证配置
python scripts/list_mcps.py status
```

## 相关资源

- [MCP官方文档](https://modelcontextprotocol.io)
- [JSON格式验证](https://jsonlint.com)
- [DeepSeek API文档](https://platform.deepseek.com/api-docs)