#!/usr/bin/env python3
"""
备份配置文件脚本
支持备份config.json和mcp.json文件
"""

import json
import os
import shutil
import sys
from datetime import datetime
from pathlib import Path

def backup_config_files(backup_dir="backup"):
    """
    备份当前目录下的config.json和mcp.json文件
    
    Args:
        backup_dir: 备份目录名称
        
    Returns:
        dict: 包含备份信息的字典
    """
    current_dir = Path.cwd()
    backup_path = current_dir / backup_dir
    
    # 创建备份目录
    backup_path.mkdir(exist_ok=True)
    
    # 生成时间戳
    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    
    backup_info = {
        "timestamp": timestamp,
        "files": [],
        "backup_dir": str(backup_path)
    }
    
    # 备份的文件列表
    files_to_backup = ["config.json", "mcp.json"]
    
    for filename in files_to_backup:
        source_file = current_dir / filename
        
        if source_file.exists():
            # 创建备份文件名
            backup_filename = f"{filename}.backup.{timestamp}"
            backup_file = backup_path / backup_filename
            
            # 复制文件
            shutil.copy2(source_file, backup_file)
            
            # 记录备份信息
            file_info = {
                "original": filename,
                "backup": backup_filename,
                "size": os.path.getsize(source_file)
            }
            backup_info["files"].append(file_info)
            
            print(f"✓ 已备份 {filename} -> {backup_filename}")
        else:
            print(f"⚠ 文件 {filename} 不存在，跳过备份")
    
    # 保存备份信息
    info_file = backup_path / f"backup_info.{timestamp}.json"
    with open(info_file, 'w', encoding='utf-8') as f:
        json.dump(backup_info, f, indent=2, ensure_ascii=False)
    
    print(f"\n📁 备份目录: {backup_path}")
    print(f"📝 备份信息: {info_file.name}")
    
    return backup_info

def list_backups(backup_dir="backup"):
    """
    列出所有备份文件
    
    Args:
        backup_dir: 备份目录名称
        
    Returns:
        list: 备份文件列表
    """
    backup_path = Path.cwd() / backup_dir
    
    if not backup_path.exists():
        print(f"备份目录 {backup_dir} 不存在")
        return []
    
    backups = []
    
    # 查找备份信息文件
    for file in backup_path.glob("backup_info.*.json"):
        try:
            with open(file, 'r', encoding='utf-8') as f:
                info = json.load(f)
                backups.append(info)
        except Exception as e:
            print(f"读取备份信息文件 {file} 时出错: {e}")
    
    # 按时间戳排序
    backups.sort(key=lambda x: x.get("timestamp", ""), reverse=True)
    
    return backups

if __name__ == "__main__":
    # 解析命令行参数
    if len(sys.argv) > 1 and sys.argv[1] == "list":
        backups = list_backups()
        
        if not backups:
            print("没有找到备份文件")
        else:
            print(f"找到 {len(backups)} 个备份:")
            for i, backup in enumerate(backups, 1):
                print(f"\n{i}. 备份时间: {backup.get('timestamp')}")
                print(f"   备份目录: {backup.get('backup_dir')}")
                for file_info in backup.get("files", []):
                    print(f"   - {file_info.get('original')} -> {file_info.get('backup')}")
    else:
        # 执行备份
        backup_info = backup_config_files()
        
        if backup_info["files"]:
            print(f"\n✅ 备份完成！共备份了 {len(backup_info['files'])} 个文件")
        else:
            print("\n⚠ 没有文件被备份")