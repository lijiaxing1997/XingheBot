#!/usr/bin/env python3
'''
简单配置检查脚本
'''

import os
import json

def main():
    print("检查 MCP 服务器配置...")
    
    # 检查 config.json
    if os.path.exists("config.json"):
        print("✅ config.json 存在")
        try:
            with open("config.json", "r") as f:
                config = json.load(f)
            
            # 检查 MCP 服务器配置
            if "mcp_servers" in config:
                print(f"✅ 找到 {len(config['mcp_servers'])} 个 MCP 服务器")
                for server in config["mcp_servers"]:
                    print(f"  服务器: {server.get('name', '未知')}")
                    print(f"    传输: {server.get('transport', '未知')}")
                    print(f"    命令: {server.get('command', '未知')}")
            else:
                print("⚠️  config.json 中没有 mcp_servers 配置")
        except Exception as e:
            print(f"❌ 读取 config.json 错误: {e}")
    else:
        print("❌ config.json 不存在")
    
    # 检查包装器脚本
    print("\n检查包装器脚本...")
    wrapper = "bin/calculator-mcp"
    if os.path.exists(wrapper):
        print(f"✅ {wrapper} 存在")
        # 检查文件内容
        with open(wrapper, "r") as f:
            content = f.read()
            if "calculator_mcp" in content:
                print("✅ 包装器脚本包含正确的导入")
            else:
                print("⚠️  包装器脚本可能不正确")
    else:
        print(f"❌ {wrapper} 不存在")
    
    # 检查 MCP 服务器文件
    print("\n检查 MCP 服务器文件...")
    server_file = "calculator-mcp/calculator_mcp.py"
    if os.path.exists(server_file):
        print(f"✅ {server_file} 存在")
    else:
        print(f"❌ {server_file} 不存在")
    
    print("\n配置检查完成！")

if __name__ == "__main__":
    main()