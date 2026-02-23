#!/usr/bin/env python3
"""
ClawHub 技能管理器验证脚本
用于验证技能的所有组件是否完整和可用
"""

import os
import sys
import subprocess
import json
from pathlib import Path

def check_file_exists(filepath, description):
    """检查文件是否存在"""
    if os.path.exists(filepath):
        print(f"✓ {description}: {filepath}")
        return True
    else:
        print(f"✗ {description}: 文件不存在 - {filepath}")
        return False

def check_file_content(filepath, min_size=100):
    """检查文件内容是否完整"""
    try:
        size = os.path.getsize(filepath)
        if size >= min_size:
            print(f"  ✓ 文件大小: {size} 字节")
            return True
        else:
            print(f"  ✗ 文件过小: {size} 字节 (最小要求: {min_size} 字节)")
            return False
    except Exception as e:
        print(f"  ✗ 无法读取文件: {e}")
        return False

def check_skill_md():
    """检查 SKILL.md 文件"""
    print("\n1. 检查 SKILL.md 文件...")
    skill_md = Path("SKILL.md")
    
    if not check_file_exists(skill_md, "SKILL.md 文件"):
        return False
    
    # 检查文件内容
    content = skill_md.read_text(encoding='utf-8')
    
    # 检查 YAML frontmatter
    if content.startswith("---"):
        print("  ✓ 包含 YAML frontmatter")
    else:
        print("  ✗ 缺少 YAML frontmatter")
        return False
    
    # 检查必要章节
    required_sections = [
        "## 概述",
        "## 前置要求", 
        "## 可用命令和示例",
        "## 使用场景",
        "## 故障排除"
    ]
    
    for section in required_sections:
        if section in content:
            print(f"  ✓ 包含章节: {section}")
        else:
            print(f"  ✗ 缺少章节: {section}")
            return False
    
    return check_file_content(skill_md, 2000)

def check_references():
    """检查参考文档"""
    print("\n2. 检查参考文档...")
    references_dir = Path("references")
    
    if not check_file_exists(references_dir, "参考文档目录"):
        return False
    
    required_refs = [
        ("command_reference.md", "命令参考文档"),
        ("usage_examples.md", "使用示例文档")
    ]
    
    all_ok = True
    for filename, description in required_refs:
        filepath = references_dir / filename
        if check_file_exists(filepath, description):
            if not check_file_content(filepath, 500):
                all_ok = False
        else:
            all_ok = False
    
    return all_ok

def check_scripts():
    """检查脚本文件"""
    print("\n3. 检查脚本文件...")
    scripts_dir = Path("scripts")
    
    if not check_file_exists(scripts_dir, "脚本目录"):
        return False
    
    required_scripts = [
        ("test_clawhub.sh", "测试脚本"),
        ("validate_skill.py", "验证脚本")
    ]
    
    all_ok = True
    for filename, description in required_scripts:
        filepath = scripts_dir / filename
        if check_file_exists(filepath, description):
            # 检查脚本是否可执行（对于 .sh 文件）
            if filename.endswith('.sh'):
                if os.access(filepath, os.X_OK):
                    print(f"  ✓ {filename} 可执行")
                else:
                    print(f"  ✗ {filename} 不可执行")
                    all_ok = False
            
            if not check_file_content(filepath, 100):
                all_ok = False
        else:
            all_ok = False
    
    return all_ok

def check_directory_structure():
    """检查目录结构"""
    print("\n4. 检查目录结构...")
    
    expected_structure = [
        "SKILL.md",
        "references/",
        "references/command_reference.md",
        "references/usage_examples.md",
        "scripts/",
        "scripts/test_clawhub.sh",
        "scripts/validate_skill.py"
    ]
    
    all_ok = True
    for item in expected_structure:
        if item.endswith('/'):
            # 检查目录
            if os.path.isdir(item.rstrip('/')):
                print(f"✓ 目录存在: {item}")
            else:
                print(f"✗ 目录不存在: {item}")
                all_ok = False
        else:
            # 检查文件
            if os.path.exists(item):
                print(f"✓ 文件存在: {item}")
            else:
                print(f"✗ 文件不存在: {item}")
                all_ok = False
    
    return all_ok

def run_test_script():
    """运行测试脚本"""
    print("\n5. 运行测试脚本...")
    test_script = Path("scripts/test_clawhub.sh")
    
    if not test_script.exists():
        print("✗ 测试脚本不存在")
        return False
    
    try:
        # 运行测试脚本
        result = subprocess.run(
            ["bash", str(test_script)],
            capture_output=True,
            text=True,
            timeout=10
        )
        
        if result.returncode == 0:
            print("✓ 测试脚本运行成功")
            # 检查输出是否包含关键信息
            if "ClawHub 技能管理器测试脚本" in result.stdout:
                print("  ✓ 测试输出格式正确")
            else:
                print("  ✗ 测试输出格式异常")
                return False
            return True
        else:
            print(f"✗ 测试脚本运行失败，退出码: {result.returncode}")
            print(f"  错误输出: {result.stderr[:200]}")
            return False
            
    except subprocess.TimeoutExpired:
        print("✗ 测试脚本执行超时")
        return False
    except Exception as e:
        print(f"✗ 运行测试脚本时出错: {e}")
        return False

def main():
    """主验证函数"""
    print("=" * 60)
    print("ClawHub 技能管理器验证")
    print("=" * 60)
    
    # 切换到技能目录
    skill_dir = Path(__file__).parent.parent
    os.chdir(skill_dir)
    print(f"工作目录: {os.getcwd()}")
    
    # 执行各项检查
    checks = [
        ("目录结构", check_directory_structure),
        ("SKILL.md 文件", check_skill_md),
        ("参考文档", check_references),
        ("脚本文件", check_scripts),
        ("测试脚本", run_test_script)
    ]
    
    results = []
    for check_name, check_func in checks:
        print(f"\n{'='*40}")
        print(f"检查: {check_name}")
        print('='*40)
        try:
            success = check_func()
            results.append((check_name, success))
        except Exception as e:
            print(f"检查过程中出错: {e}")
            results.append((check_name, False))
    
    # 汇总结果
    print(f"\n{'='*60}")
    print("验证结果汇总")
    print('='*60)
    
    all_passed = True
    for check_name, success in results:
        status = "✓ 通过" if success else "✗ 失败"
        print(f"{check_name}: {status}")
        if not success:
            all_passed = False
    
    print(f"\n{'='*60}")
    if all_passed:
        print("🎉 所有检查通过！技能完整可用。")
        return 0
    else:
        print("❌ 部分检查未通过，请修复上述问题。")
        return 1

if __name__ == "__main__":
    sys.exit(main())