# 配置文件结构说明

## config.json 结构

`config.json` 文件包含DeepSeek API的配置信息：

```json
{
  "api_key": "sk-3b7dd428d530441797a87ff41bf98258",
  "base_url": "https://api.deepseek.com",
  "model": "deepseek-chat",
  "max_tokens": 8192
}
```

### 字段说明

| 字段 | 类型 | 说明 | 示例 |
|------|------|------|------|
| `api_key` | string | DeepSeek API密钥 | `"sk-3b7dd428d530441797a87ff41bf98258"` |
| `base_url` | string | API基础URL | `"https://api.deepseek.com"` |
| `model` | string | 使用的模型名称 | `"deepseek-chat"` |
| `max_tokens` | integer | 最大token数 | `8192` |

### 注意事项

1. **API密钥安全**：API密钥是敏感信息，不应公开分享
2. **模型选择**：根据需求选择合适的模型
3. **Token限制**：根据模型能力设置合适的max_tokens值

## mcp.json 结构

`mcp.json` 文件包含MCP（Model Context Protocol）服务器的配置信息：

```json
{
  "mcp_servers": [
    {
      "name": "calculator",
      "transport": "command",
      "command": "./bin/calculator-mcp",
      "args": [],
      "env": {
        "PYTHONPATH": "${PYTHONPATH}:./mcp/calculator"
      }
    }
  ]
}
```

### mcp_servers 数组

每个MCP服务器配置包含以下字段：

| 字段 | 类型 | 说明 | 必需 | 示例 |
|------|------|------|------|------|
| `name` | string | 服务器名称（唯一标识） | 是 | `"calculator"` |
| `transport` | string | 传输协议类型 | 是 | `"command"`, `"stdio"`, `"sse"` |
| `command` | string | 启动服务器的命令 | 是 | `"./bin/calculator-mcp"` |
| `args` | array | 命令参数 | 否 | `["--verbose", "--port=8080"]` |
| `env` | object | 环境变量 | 否 | `{"PYTHONPATH": "...", "FOO": "bar"}` |

### 传输类型说明

1. **command**：通过命令行启动服务器
2. **stdio**：通过标准输入输出通信
3. **sse**：通过Server-Sent Events通信

### 环境变量

环境变量支持使用 `${VARIABLE}` 语法引用现有环境变量：

```json
{
  "env": {
    "PYTHONPATH": "${PYTHONPATH}:./mcp/calculator",
    "CUSTOM_PATH": "/opt/myapp:${PATH}"
  }
}
```

## mcp.exm.json 结构

`mcp.exm.json` 是示例配置文件，展示了完整的MCP服务器配置：

```json
{
  "mcp_servers": [
    {
      "name": "greeter",
      "transport": "command",
      "command": "./bin/greeter",
      "args": [],
      "env": {
        "FOO": "bar"
      }
    },
    {
      "name": "calculator",
      "transport": "command",
      "command": "./bin/calculator-mcp",
      "args": [],
      "env": {
        "PYTHONPATH": "${PYTHONPATH}:./mcp/calculator"
      }
    }
  ]
}
```

### 示例说明

1. **greeter**：一个简单的问候服务器示例
2. **calculator**：计算器功能服务器

## 配置文件位置

所有配置文件都应位于项目根目录：

```
项目根目录/
├── config.json          # API配置
├── mcp.json            # 当前MCP配置
├── mcp.exm.json        # 示例MCP配置
└── backup/             # 备份目录（自动创建）
    ├── config.json.backup.20240101_120000
    ├── mcp.json.backup.20240101_120000
    └── backup_info.20240101_120000.json
```

## 最佳实践

1. **定期备份**：在修改配置前进行备份
2. **版本控制**：将配置文件添加到.gitignore，避免提交敏感信息
3. **环境分离**：为不同环境（开发、测试、生产）使用不同的配置
4. **验证配置**：修改配置后验证格式是否正确

## 常见问题

### Q: 配置文件格式错误怎么办？
A: 使用JSON验证工具检查格式，或从备份恢复

### Q: 如何添加新的MCP服务器？
A: 参考mcp.exm.json中的示例，在mcp.json的mcp_servers数组中添加新配置

### Q: API密钥泄露了怎么办？
A: 立即在DeepSeek平台重置API密钥，并更新config.json

### Q: 如何迁移配置到新环境？
A: 复制config.json和mcp.json文件，根据需要调整路径和环境变量