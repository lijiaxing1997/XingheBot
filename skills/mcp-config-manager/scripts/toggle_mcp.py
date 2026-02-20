#!/usr/bin/env python3
"""
启用/禁用MCP服务器脚本
支持通过名称启用或禁用特定的MCP服务器
"""

import json
import sys
from pathlib import Path

def load_mcp_config():
    """加载mcp.json配置文件"""
    config_file = Path("mcp.json")
    
    if not config_file.exists():
        print(f"错误: 找不到 {config_file}")
        return None
    
    try:
        with open(config_file, 'r', encoding='utf-8') as f:
            return json.load(f)
    except Exception as e:
        print(f"读取 {config_file} 时出错: {e}")
        return None

def save_mcp_config(config):
    """保存mcp.json配置文件"""
    config_file = Path("mcp.json")
    
    try:
        with open(config_file, 'w', encoding='utf-8') as f:
            json.dump(config, f, indent=2, ensure_ascii=False)
        return True
    except Exception as e:
        print(f"保存 {config_file} 时出错: {e}")
        return False

def list_mcp_servers():
    """列出所有MCP服务器"""
    config = load_mcp_config()
    
    if not config:
        return []
    
    servers = config.get("mcp_servers", [])
    
    print(f"当前配置了 {len(servers)} 个MCP服务器:")
    for i, server in enumerate(servers, 1):
        name = server.get("name", "未命名")
        transport = server.get("transport", "未知")
        command = server.get("command", "")
        
        print(f"{i}. {name}")
        print(f"   传输方式: {transport}")
        print(f"   命令: {command}")
        
        if "args" in server and server["args"]:
            print(f"   参数: {server['args']}")
        
        if "env" in server and server["env"]:
            print(f"   环境变量: {server['env']}")
        
        print()
    
    return servers

def enable_mcp_server(server_name):
    """启用指定的MCP服务器"""
    config = load_mcp_config()
    
    if not config:
        return False
    
    servers = config.get("mcp_servers", [])
    
    # 检查服务器是否存在
    server_found = False
    for server in servers:
        if server.get("name") == server_name:
            server_found = True
            print(f"✅ MCP服务器 '{server_name}' 已启用")
            break
    
    if not server_found:
        print(f"⚠ 未找到名为 '{server_name}' 的MCP服务器")
        return False
    
    # 保存配置
    if save_mcp_config(config):
        print(f"✅ 配置已保存到 mcp.json")
        return True
    else:
        return False

def disable_mcp_server(server_name):
    """禁用指定的MCP服务器（从配置中移除）"""
    config = load_mcp_config()
    
    if not config:
        return False
    
    servers = config.get("mcp_servers", [])
    
    # 查找并移除服务器
    new_servers = []
    removed = False
    
    for server in servers:
        if server.get("name") == server_name:
            removed = True
            print(f"✅ MCP服务器 '{server_name}' 已从配置中移除")
        else:
            new_servers.append(server)
    
    if not removed:
        print(f"⚠ 未找到名为 '{server_name}' 的MCP服务器")
        return False
    
    # 更新配置
    config["mcp_servers"] = new_servers
    
    # 保存配置
    if save_mcp_config(config):
        print(f"✅ 配置已保存到 mcp.json")
        print(f"📊 剩余 {len(new_servers)} 个MCP服务器")
        return True
    else:
        return False

def add_mcp_server(server_config):
    """添加新的MCP服务器"""
    config = load_mcp_config()
    
    if not config:
        return False
    
    servers = config.get("mcp_servers", [])
    
    # 检查是否已存在同名服务器
    server_name = server_config.get("name")
    for server in servers:
        if server.get("name") == server_name:
            print(f"⚠ 已存在名为 '{server_name}' 的MCP服务器")
            return False
    
    # 添加新服务器
    servers.append(server_config)
    config["mcp_servers"] = servers
    
    # 保存配置
    if save_mcp_config(config):
        print(f"✅ 已添加MCP服务器 '{server_name}'")
        print(f"📊 当前共有 {len(servers)} 个MCP服务器")
        return True
    else:
        return False

if __name__ == "__main__":
    # 解析命令行参数
    if len(sys.argv) < 2:
        print("用法:")
        print("  python toggle_mcp.py list                    # 列出所有MCP服务器")
        print("  python toggle_mcp.py enable <server_name>    # 启用MCP服务器")
        print("  python toggle_mcp.py disable <server_name>   # 禁用MCP服务器")
        print("  python toggle_mcp.py add <config_json>       # 添加MCP服务器")
        sys.exit(1)
    
    command = sys.argv[1]
    
    if command == "list":
        list_mcp_servers()
    
    elif command == "enable":
        if len(sys.argv) < 3:
            print("错误: 请指定要启用的MCP服务器名称")
            sys.exit(1)
        
        server_name = sys.argv[2]
        enable_mcp_server(server_name)
    
    elif command == "disable":
        if len(sys.argv) < 3:
            print("错误: 请指定要禁用的MCP服务器名称")
            sys.exit(1)
        
        server_name = sys.argv[2]
        disable_mcp_server(server_name)
    
    elif command == "add":
        if len(sys.argv) < 3:
            print("错误: 请提供MCP服务器配置的JSON字符串")
            sys.exit(1)
        
        try:
            server_config = json.loads(sys.argv[2])
            add_mcp_server(server_config)
        except json.JSONDecodeError as e:
            print(f"错误: 无效的JSON格式: {e}")
        except Exception as e:
            print(f"错误: {e}")
    
    else:
        print(f"错误: 未知命令 '{command}'")
        sys.exit(1)