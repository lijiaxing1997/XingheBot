# MCP 服务器集成指南

## 概述

本文档说明如何将开发的 MCP 服务器集成到当前项目中，使其工具可供 AI 代理使用。

## 项目结构

```
项目根目录/
├── bin/                    # 可执行脚本
│   ├── agent              # 主代理程序
│   └── calculator-mcp     # Calculator MCP 服务器包装器
├── calculator-mcp/        # Calculator MCP 服务器
│   ├── calculator_mcp.py  # 主服务器文件
│   ├── requirements.txt   # Python 依赖
│   └── ...               # 其他文件
├── config.json           # 主配置文件
├── config.exm.json      # 配置示例文件
└── ...                  # 其他项目文件
```

## 集成步骤

### 步骤 1: 创建包装器脚本

在 `bin/` 目录下创建包装器脚本，用于运行你的 MCP 服务器：

**示例: `bin/calculator-mcp`**
```python
#!/usr/bin/env python3
'''
Calculator MCP Server wrapper script
'''

import os
import sys

# 添加父目录到 Python 路径
parent_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
calculator_dir = os.path.join(parent_dir, "calculator-mcp")

# 添加 calculator-mcp 目录到 Python 路径
sys.path.insert(0, calculator_dir)

# 切换到 calculator 目录
os.chdir(calculator_dir)

# 导入并运行 MCP 服务器
from calculator_mcp import mcp

if __name__ == "__main__":
    mcp.run()
```

**设置执行权限:**
```bash
chmod +x bin/calculator-mcp
```

### 步骤 2: 更新配置文件

编辑 `config.json` 文件，添加 MCP 服务器配置：

**当前配置示例:**
```json
{
  "api_key": "sk-3b7dd428d530441797a87ff41bf98258",
  "base_url": "https://api.deepseek.com",
  "model": "deepseek-chat",
  "max_tokens": 8192,
  "mcp_servers": [
    {
      "name": "calculator",
      "transport": "command",
      "command": "./bin/calculator-mcp",
      "args": [],
      "env": {
        "PYTHONPATH": "${PYTHONPATH}:./calculator-mcp"
      }
    }
  ]
}
```

### 步骤 3: 配置选项详解

#### 传输类型 (transport)

1. **`"command"`** - 子进程模式 (用于 stdio 服务器)
   - 适用于本地运行的 Python/Node.js 服务器
   - 通过标准输入输出与代理通信

2. **`"http"`** - HTTP 模式
   - 适用于远程 HTTP 服务器
   - 通过 HTTP 请求与代理通信

#### Command 传输配置

```json
{
  "name": "server-name",          // 服务器唯一标识
  "transport": "command",         // 必须为 "command"
  "command": "./bin/script",      // 可执行文件或脚本路径
  "args": ["--arg1", "value1"],   // 命令行参数 (可选)
  "env": {                        // 环境变量 (可选)
    "PYTHONPATH": "${PYTHONPATH}:./server-dir",
    "API_KEY": "your-api-key"
  },
  "dir": "./server-dir",          // 工作目录 (可选)
  "inherit_env": true             // 是否继承父环境 (可选，默认 true)
}
```

#### HTTP 传输配置

```json
{
  "name": "server-name",
  "transport": "http",
  "url": "http://localhost:8000",
  "headers": {
    "Authorization": "Bearer your-token"
  }
}
```

### 步骤 4: 测试集成

#### 启动代理
```bash
./bin/agent chat
```

#### 验证 MCP 工具可用性
询问代理：
- "What MCP tools are available?"
- "List available tools"
- "What calculator tools do you have?"

#### 测试具体工具
```bash
# 通过代理使用工具
./bin/agent chat

# 在聊天中测试
用户: Calculate 15 + 27
用户: What is the square root of 144?
用户: Convert 100°C to Fahrenheit
```

## Calculator MCP 服务器示例

### 已实现的工具

1. **`calculator_basic_operation`** - 基础算术运算
   - 操作: "add", "subtract", "multiply", "divide"
   - 示例: `{"operation": "add", "a": 5, "b": 3}`

2. **`calculator_advanced_math`** - 高级数学函数
   - 操作: "power", "square_root", "logarithm", "exponential" 等
   - 示例: `{"operation": "square_root", "value": 16}`

3. **`calculator_trigonometric`** - 三角函数
   - 操作: "sine", "cosine", "tangent", "arcsine" 等
   - 示例: `{"operation": "sine", "angle": 30}`

4. **`calculator_statistics`** - 统计计算
   - 操作: "mean", "median", "stdev", "variance" 等
   - 示例: `{"operation": "mean", "values": [1, 2, 3, 4, 5]}`

5. **`calculator_unit_conversion`** - 单位转换
   - 单位类型: "temperature", "length", "weight"
   - 示例: `{"unit_type": "temperature", "value": 100, "from_unit": "celsius", "to_unit": "fahrenheit"}`

### 测试脚本

运行测试验证功能：
```bash
cd calculator-mcp
python test_calculator.py
python example_usage.py
```

## 故障排除

### 常见问题

#### 1. "command not found" 错误
- 检查脚本是否有执行权限：`chmod +x bin/your-script`
- 验证 shebang 行：`#!/usr/bin/env python3`
- 确保 Python 3 已安装：`python3 --version`

#### 2. 导入错误 (ImportError)
- 检查 Python 路径配置
- 验证依赖是否安装：`pip install -r requirements.txt`
- 确保模块在正确的目录中

#### 3. MCP 服务器未显示
- 检查 config.json 语法
- 验证服务器名称不冲突
- 确保传输类型正确 ("command" 或 "http")

#### 4. 连接错误
- 独立测试服务器：`python calculator_mcp.py`
- 检查 Python 语法错误
- 验证所有必需模块已导入

#### 5. 权限问题
```bash
# 检查文件权限
ls -la bin/calculator-mcp

# 设置正确权限
chmod 755 bin/calculator-mcp

# 检查 Python 可访问性
which python3
```

### 调试步骤

1. **独立运行服务器:**
```bash
cd calculator-mcp
python calculator_mcp.py
# 应该启动并等待连接，无错误信息
```

2. **测试包装器脚本:**
```bash
./bin/calculator-mcp
# 应该与直接运行相同
```

3. **检查配置文件:**
```bash
# 验证 JSON 语法
python -m json.tool config.json
```

4. **查看代理日志:**
```bash
# 运行代理并观察输出
./bin/agent chat 2>&1 | grep -i "mcp\|calculator\|error"
```

## 最佳实践

### 命名约定
- **服务器名称**: 使用描述性名称 (如 "calculator", "github", "jira")
- **工具名称**: 应包含服务器前缀 (如 "calculator_add", "github_create_issue")
- **配置文件**: 保持一致的命名和结构

### 错误处理
- MCP 服务器应提供清晰的错误消息
- 优雅处理边界情况
- 记录错误以便调试

### 性能优化
- 对 I/O 操作使用 async/await
- 为外部调用实现超时
- 适当缓存常用数据

### 安全性
- 验证所有输入
- 清理输出
- 对敏感数据使用环境变量
- 需要时实现速率限制

## 扩展指南

### 添加新的 MCP 服务器

1. **开发服务器:**
   - 在单独目录中开发 MCP 服务器
   - 遵循 MCP 协议规范
   - 实现工具和资源

2. **创建包装器:**
   - 在 `bin/` 中创建包装器脚本
   - 设置正确的 Python 路径
   - 添加执行权限

3. **更新配置:**
   - 在 `config.json` 的 `mcp_servers` 数组中添加新配置
   - 使用唯一的服务器名称
   - 配置正确的传输类型和参数

4. **测试验证:**
   - 独立测试服务器
   - 通过包装器测试
   - 在代理中验证工具可用性

### 配置多个 MCP 服务器

```json
{
  "mcp_servers": [
    {
      "name": "calculator",
      "transport": "command",
      "command": "./bin/calculator-mcp"
    },
    {
      "name": "github",
      "transport": "command", 
      "command": "./bin/github-mcp"
    },
    {
      "name": "weather",
      "transport": "http",
      "url": "http://localhost:8080"
    }
  ]
}
```

## 总结

通过遵循本指南，你可以：
1. 成功将 MCP 服务器集成到项目中
2. 使 MCP 工具可供 AI 代理使用
3. 调试和解决常见问题
4. 扩展项目以支持多个 MCP 服务器

Calculator MCP 服务器已成功集成，现在可以通过代理使用所有计算器工具进行数学运算和单位转换。