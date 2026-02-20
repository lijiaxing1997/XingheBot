#!/usr/bin/env python3
'''
验证 MCP 服务器集成配置

这个脚本验证：
1. 配置文件语法
2. 包装器脚本存在性和权限
3. MCP 服务器可运行性
4. 依赖项安装
'''

import os
import sys
import json
import subprocess
import stat

def check_file_exists(filepath, description):
    '''检查文件是否存在'''
    if os.path.exists(filepath):
        print(f"✅ {description}: {filepath}")
        return True
    else:
        print(f"❌ {description}: {filepath} (文件不存在)")
        return False

def check_file_executable(filepath, description):
    '''检查文件是否可执行'''
    if not os.path.exists(filepath):
        print(f"❌ {description}: {filepath} (文件不存在)")
        return False
    
    # 检查执行权限
    st = os.stat(filepath)
    if st.st_mode & stat.S_IEXEC:
        print(f"✅ {description}: {filepath} (可执行)")
        return True
    else:
        print(f"❌ {description}: {filepath} (不可执行)")
        return False

def check_json_syntax(filepath, description):
    '''检查 JSON 文件语法'''
    if not os.path.exists(filepath):
        print(f"❌ {description}: {filepath} (文件不存在)")
        return False
    
    try:
        with open(filepath, 'r', encoding='utf-8') as f:
            data = json.load(f)
        print(f"✅ {description}: {filepath} (JSON 语法正确)")
        return True
    except json.JSONDecodeError as e:
        print(f"❌ {description}: {filepath} (JSON 语法错误: {e})")
        return False
    except Exception as e:
        print(f"❌ {description}: {filepath} (读取错误: {e})")
        return False

def check_python_script(filepath, description):
    '''检查 Python 脚本语法'''
    if not os.path.exists(filepath):
        print(f"❌ {description}: {filepath} (文件不存在)")
        return False
    
    try:
        result = subprocess.run(
            [sys.executable, "-m", "py_compile", filepath],
            capture_output=True,
            text=True
        )
        if result.returncode == 0:
            print(f"✅ {description}: {filepath} (Python 语法正确)")
            return True
        else:
            print(f"❌ {description}: {filepath} (Python 语法错误)")
            print(f"   错误信息: {result.stderr}")
            return False
    except Exception as e:
        print(f"❌ {description}: {filepath} (检查错误: {e})")
        return False

def check_mcp_server_config(config_path):
    '''检查 MCP 配置文件（mcp_servers）'''
    if not os.path.exists(config_path):
        print(f"❌ 配置文件不存在: {config_path}")
        return False
    
    try:
        with open(config_path, 'r', encoding='utf-8') as f:
            config = json.load(f)
        
        print(f"📋 配置文件: {config_path}")
        
        # 检查 MCP 服务器配置
        if 'mcp_servers' in config:
            print(f"  ✅ mcp_servers: 找到 {len(config['mcp_servers'])} 个服务器")
            
            for i, server in enumerate(config['mcp_servers']):
                print(f"   服务器 #{i+1}:")
                print(f"     name: {server.get('name', '(缺失)')}")
                print(f"     transport: {server.get('transport', '(缺失)')}")
                print(f"     command: {server.get('command', '(缺失)')}")
                
                # 检查 calculator 服务器
                if server.get('name') == 'calculator':
                    command = server.get('command', '')
                    if command == './bin/calculator-mcp':
                        print(f"     ✅ calculator 服务器配置正确")
                    else:
                        print(f"     ⚠️  calculator 服务器命令可能不正确: {command}")
        else:
            print(f"  ⚠️  mcp_servers: (缺失 - 将无法使用 MCP 服务器)")
        
        return True
        
    except Exception as e:
        print(f"❌ 读取配置文件错误: {e}")
        return False

def check_calculator_mcp_server():
    '''检查 Calculator MCP 服务器'''
    print("\n🔧 检查 Calculator MCP 服务器")
    
    # 检查包装器脚本
    wrapper_path = "./bin/calculator-mcp"
    if not check_file_exists(wrapper_path, "包装器脚本"):
        return False
    
    if not check_file_executable(wrapper_path, "包装器脚本"):
        print("  尝试修复权限...")
        try:
            os.chmod(wrapper_path, 0o755)
            print("  权限已修复")
        except Exception as e:
            print(f"  修复权限失败: {e}")
    
    # 检查主服务器文件
    server_path = "./mcp/calculator/calculator_mcp.py"
    if not check_file_exists(server_path, "MCP 服务器文件"):
        return False
    
    if not check_python_script(server_path, "MCP 服务器文件"):
        return False
    
    # 检查依赖文件
    requirements_path = "./mcp/calculator/requirements.txt"
    if check_file_exists(requirements_path, "依赖文件"):
        print(f"  📦 依赖文件: {requirements_path}")
        try:
            with open(requirements_path, 'r') as f:
                deps = f.read().strip().split('\n')
                for dep in deps:
                    if dep.strip():
                        print(f"    - {dep.strip()}")
        except:
            pass
    
    return True

def check_python_dependencies():
    '''检查 Python 依赖'''
    print("\n🐍 检查 Python 依赖")
    
    dependencies = ['mcp', 'pydantic', 'httpx']
    
    for dep in dependencies:
        try:
            __import__(dep)
            print(f"  ✅ {dep}: 已安装")
        except ImportError:
            print(f"  ❌ {dep}: 未安装")
            print(f"     安装命令: pip install {dep}")
    
    # 检查 numpy (可选)
    try:
        import numpy
        print(f"  ✅ numpy: 已安装 (可选)")
    except ImportError:
        print(f"  ⚠️  numpy: 未安装 (可选依赖)")

def run_quick_test():
    '''运行快速测试'''
    print("\n🧪 运行快速测试")
    
    # 测试包装器脚本
    wrapper_path = "./bin/calculator-mcp"
    if os.path.exists(wrapper_path):
        print("  测试包装器脚本...")
        try:
            # 检查 shebang
            with open(wrapper_path, 'r') as f:
                first_line = f.readline().strip()
                if first_line == "#!/usr/bin/env bash":
                    print("    ✅ Shebang 正确")
                else:
                    print(f"    ⚠️  Shebang 可能不正确: {first_line}")
            
            # 测试导入
            test_code = """
import sys
sys.path.insert(0, './mcp/calculator')
try:
    from calculator_mcp import mcp
    print("    ✅ 可以导入 MCP 服务器")
except Exception as e:
    print(f"    ❌ 导入错误: {e}")
"""
            result = subprocess.run(
                [sys.executable, "-c", test_code],
                capture_output=True,
                text=True,
                cwd="."
            )
            if result.stdout:
                print(result.stdout.strip())
            
        except Exception as e:
            print(f"    ❌ 测试错误: {e}")
    else:
        print("  ⚠️  包装器脚本不存在，跳过测试")

def main():
    '''主验证函数'''
    print("=" * 60)
    print("MCP 服务器集成验证")
    print("=" * 60)
    
    # 获取当前目录
    current_dir = os.getcwd()
    print(f"当前目录: {current_dir}")
    
    # 检查配置文件
    print("\n📄 检查配置文件")
    config_files = ['config.json', 'config.exm.json', 'mcp.json', 'mcp.exm.json']
    for config_file in config_files:
        check_json_syntax(config_file, f"配置文件 {config_file}")
    
    # 检查 MCP 配置文件内容
    check_mcp_server_config("mcp.json")
    
    # 检查 Calculator MCP 服务器
    calculator_ok = check_calculator_mcp_server()
    
    # 检查 Python 依赖
    check_python_dependencies()
    
    # 运行快速测试
    run_quick_test()
    
    # 总结
    print("\n" + "=" * 60)
    print("验证总结")
    print("=" * 60)
    
    if calculator_ok:
        print("✅ Calculator MCP 服务器配置基本正确")
        print("\n下一步:")
        print("1. 安装依赖: pip install -r mcp/calculator/requirements.txt")
        print("2. 测试服务器: cd mcp/calculator && python test_calculator.py")
        print("3. 启动代理: ./bin/agent chat")
        print("4. 测试工具: 询问 'What calculator tools are available?'")
    else:
        print("❌ 存在配置问题，请检查上述错误")
    
    print("\n详细指南请查看:")
    print("- MCP_INTEGRATION_GUIDE.md")
    print("- mcp/calculator/README.md")
    print("- mcp/calculator/INSTALL.md")

if __name__ == "__main__":
    main()
