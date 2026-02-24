#!/usr/bin/env python3
"""
列出MCP服务器和配置信息脚本
提供详细的MCP服务器和配置信息
"""

import json
from pathlib import Path

def load_json_file(filename):
    """加载JSON文件"""
    file_path = Path(filename)
    
    if not file_path.exists():
        print(f"⚠ 文件 {filename} 不存在")
        return None
    
    try:
        with open(file_path, 'r', encoding='utf-8') as f:
            return json.load(f)
    except Exception as e:
        print(f"读取 {filename} 时出错: {e}")
        return None

def mask_secret(value: str) -> str:
    """隐藏敏感信息的部分内容"""
    if not value:
        return ""
    if len(value) <= 12:
        return "***"
    return value[:8] + "..." + value[-4:]

def show_config_info():
    """显示config.json信息"""
    config = load_json_file("config.json")
    
    if not config:
        return
    
    print("📋 config.json 配置信息:")
    print("=" * 50)

    model_config = config.get("model_config", {})
    if isinstance(model_config, dict) and model_config:
        print("  model_config:")
        for key, value in model_config.items():
            if key == "api_key" and isinstance(value, str) and value:
                print(f"    {key}: {mask_secret(value)}")
            else:
                print(f"    {key}: {value}")
    else:
        # Back-compat for legacy flat keys.
        legacy_keys = ["api_key", "base_url", "model", "max_tokens"]
        for key in legacy_keys:
            if key not in config:
                continue
            value = config.get(key)
            if key == "api_key" and isinstance(value, str) and value:
                print(f"  {key}: {mask_secret(value)}")
            else:
                print(f"  {key}: {value}")

    web_search = config.get("web_search", {})
    if isinstance(web_search, dict) and web_search:
        print("  web_search:")
        for key, value in web_search.items():
            if key.endswith("_api_key") and isinstance(value, str) and value:
                print(f"    {key}: {mask_secret(value)}")
            else:
                print(f"    {key}: {value}")
    elif "tavily_api_key" in config:
        value = config.get("tavily_api_key")
        if isinstance(value, str) and value:
            print(f"  tavily_api_key: {mask_secret(value)}")

    ignored = {"model_config", "web_search", "api_key", "base_url", "model", "max_tokens", "tavily_api_key"}
    other_keys = sorted([k for k in config.keys() if k not in ignored])
    if other_keys:
        print("  other:")
        for k in other_keys:
            v = config.get(k)
            if isinstance(v, dict):
                print(f"    {k}: object ({len(v)} keys)")
            elif isinstance(v, list):
                print(f"    {k}: array ({len(v)} items)")
            else:
                print(f"    {k}: {v}")
    
    print()

def show_mcp_info():
    """显示mcp.json信息"""
    config = load_json_file("mcp.json")
    
    if not config:
        return
    
    servers = config.get("mcp_servers", [])
    
    print("🔧 MCP服务器配置:")
    print("=" * 50)
    
    if not servers:
        print("  当前没有配置MCP服务器")
        return
    
    print(f"  共配置了 {len(servers)} 个MCP服务器:\n")
    
    for i, server in enumerate(servers, 1):
        name = server.get("name", "未命名")
        transport = server.get("transport", "未知")
        command = server.get("command", "")
        
        print(f"  {i}. {name}")
        print(f"     传输方式: {transport}")
        print(f"     命令: {command}")
        
        if "args" in server and server["args"]:
            print(f"     参数: {server['args']}")
        
        if "env" in server and server["env"]:
            print(f"     环境变量:")
            for env_key, env_value in server["env"].items():
                print(f"       {env_key}={env_value}")
        
        print()

def show_example_mcp_info():
    """显示示例mcp配置信息"""
    config = load_json_file("mcp.exm.json")
    
    if not config:
        return
    
    servers = config.get("mcp_servers", [])
    
    print("📚 示例MCP服务器配置 (mcp.exm.json):")
    print("=" * 50)
    
    if not servers:
        print("  示例文件中没有MCP服务器配置")
        return
    
    print(f"  示例中共有 {len(servers)} 个MCP服务器:\n")
    
    for i, server in enumerate(servers, 1):
        name = server.get("name", "未命名")
        transport = server.get("transport", "未知")
        command = server.get("command", "")
        
        print(f"  {i}. {name}")
        print(f"     传输方式: {transport}")
        print(f"     命令: {command}")
        
        if "args" in server and server["args"]:
            print(f"     参数: {server['args']}")
        
        if "env" in server and server["env"]:
            print(f"     环境变量:")
            for env_key, env_value in server["env"].items():
                print(f"       {env_key}={env_value}")
        
        print()

def check_mcp_status():
    """检查MCP服务器状态"""
    import subprocess
    import shutil
    
    print("🔍 MCP服务器状态检查:")
    print("=" * 50)
    
    config = load_json_file("mcp.json")
    
    if not config:
        return
    
    servers = config.get("mcp_servers", [])
    
    for server in servers:
        name = server.get("name", "未命名")
        command = server.get("command", "")
        
        print(f"  {name}:")
        
        # 检查命令是否存在
        if command:
            # 提取可执行文件路径
            cmd_path = command.split()[0] if ' ' in command else command
            
            # 检查文件是否存在
            if Path(cmd_path).exists():
                print(f"     命令文件: ✅ 存在 ({cmd_path})")
            else:
                # 检查是否在PATH中
                full_path = shutil.which(cmd_path)
                if full_path:
                    print(f"     命令文件: ✅ 在PATH中 ({full_path})")
                else:
                    print(f"     命令文件: ❌ 未找到 ({cmd_path})")
        else:
            print(f"     命令: ❌ 未配置")
        
        print()

if __name__ == "__main__":
    import sys
    
    # 解析命令行参数
    show_all = True
    if len(sys.argv) > 1:
        show_all = False
        command = sys.argv[1]
        
        if command == "config":
            show_config_info()
        elif command == "mcp":
            show_mcp_info()
        elif command == "example":
            show_example_mcp_info()
        elif command == "status":
            check_mcp_status()
        elif command == "all":
            show_all = True
        else:
            print(f"未知命令: {command}")
            print("可用命令: config, mcp, example, status, all")
            sys.exit(1)
    
    if show_all:
        show_config_info()
        show_mcp_info()
        show_example_mcp_info()
        check_mcp_status()
