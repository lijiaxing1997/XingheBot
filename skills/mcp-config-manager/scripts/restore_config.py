#!/usr/bin/env python3
"""
恢复配置文件脚本
支持从备份恢复config.json和mcp.json文件
"""

import json
import shutil
import sys
from datetime import datetime
from pathlib import Path

def list_backups(backup_dir="backup"):
    """
    列出所有备份文件
    
    Args:
        backup_dir: 备份目录名称
        
    Returns:
        list: 备份文件列表，按时间戳排序
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
                info["info_file"] = str(file)
                backups.append(info)
        except Exception as e:
            print(f"读取备份信息文件 {file} 时出错: {e}")
    
    # 按时间戳排序（最新的在前）
    backups.sort(key=lambda x: x.get("timestamp", ""), reverse=True)
    
    return backups

def show_backup_list(backups):
    """显示备份列表"""
    if not backups:
        print("没有找到备份文件")
        return
    
    print(f"找到 {len(backups)} 个备份:")
    print("=" * 60)
    
    for i, backup in enumerate(backups, 1):
        timestamp = backup.get("timestamp", "未知时间")
        backup_dir = backup.get("backup_dir", "未知目录")
        
        print(f"{i}. 备份时间: {timestamp}")
        print(f"   备份目录: {backup_dir}")
        
        files = backup.get("files", [])
        for file_info in files:
            original = file_info.get("original", "未知文件")
            backup_file = file_info.get("backup", "未知备份")
            size = file_info.get("size", 0)
            
            print(f"   - {original} ({size} bytes) -> {backup_file}")
        
        print()

def restore_from_backup(backup_index, backup_dir="backup", confirm=True):
    """
    从指定备份恢复文件
    
    Args:
        backup_index: 备份索引（从1开始）
        backup_dir: 备份目录名称
        confirm: 是否要求确认
        
    Returns:
        bool: 恢复是否成功
    """
    backups = list_backups(backup_dir)
    
    if not backups:
        print("没有可用的备份")
        return False
    
    if backup_index < 1 or backup_index > len(backups):
        print(f"错误: 备份索引 {backup_index} 无效，有效范围: 1-{len(backups)}")
        return False
    
    backup = backups[backup_index - 1]
    timestamp = backup.get("timestamp", "未知时间")
    backup_path = Path(backup.get("backup_dir", backup_dir))
    files = backup.get("files", [])
    
    print(f"准备从备份恢复 (时间: {timestamp}):")
    print("=" * 50)
    
    # 显示要恢复的文件
    for file_info in files:
        original = file_info.get("original", "未知文件")
        backup_file = file_info.get("backup", "未知备份")
        
        source_file = backup_path / backup_file
        target_file = Path.cwd() / original
        
        print(f"  {original}")
        print(f"    ← {backup_file}")
        
        if target_file.exists():
            current_size = target_file.stat().st_size
            backup_size = file_info.get("size", 0)
            print(f"    ⚠ 目标文件已存在 ({current_size} bytes)")
            print(f"    📊 备份大小: {backup_size} bytes")
        print()
    
    # 确认恢复
    if confirm:
        response = input("是否确认恢复？(y/N): ").strip().lower()
        if response != 'y':
            print("恢复已取消")
            return False
    
    # 执行恢复
    restored_files = []
    
    for file_info in files:
        original = file_info.get("original", "未知文件")
        backup_file = file_info.get("backup", "未知备份")
        
        source_file = backup_path / backup_file
        target_file = Path.cwd() / original
        
        if not source_file.exists():
            print(f"⚠ 备份文件 {backup_file} 不存在，跳过")
            continue
        
        try:
            # 备份当前文件（如果存在）
            if target_file.exists():
                current_timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
                current_backup = target_file.parent / f"{original}.before_restore.{current_timestamp}"
                shutil.copy2(target_file, current_backup)
                print(f"  ✓ 已备份当前 {original} -> {current_backup.name}")
            
            # 恢复文件
            shutil.copy2(source_file, target_file)
            restored_files.append(original)
            print(f"  ✅ 已恢复 {original}")
            
        except Exception as e:
            print(f"  ❌ 恢复 {original} 时出错: {e}")
    
    if restored_files:
        print(f"\n✅ 恢复完成！共恢复了 {len(restored_files)} 个文件:")
        for filename in restored_files:
            print(f"  - {filename}")
        return True
    else:
        print("\n⚠ 没有文件被恢复")
        return False

def restore_specific_file(filename, backup_dir="backup", timestamp=None):
    """
    恢复特定文件
    
    Args:
        filename: 要恢复的文件名（如 config.json）
        backup_dir: 备份目录名称
        timestamp: 指定时间戳的备份（可选）
        
    Returns:
        bool: 恢复是否成功
    """
    backups = list_backups(backup_dir)
    
    if not backups:
        print("没有可用的备份")
        return False
    
    # 如果指定了时间戳，查找对应的备份
    target_backup = None
    if timestamp:
        for backup in backups:
            if backup.get("timestamp") == timestamp:
                target_backup = backup
                break
        
        if not target_backup:
            print(f"未找到时间戳为 {timestamp} 的备份")
            return False
    else:
        # 使用最新的备份
        target_backup = backups[0]
    
    # 在备份中查找文件
    backup_path = Path(target_backup.get("backup_dir", backup_dir))
    files = target_backup.get("files", [])
    
    backup_file = None
    for file_info in files:
        if file_info.get("original") == filename:
            backup_file = file_info.get("backup")
            break
    
    if not backup_file:
        print(f"在备份中未找到 {filename}")
        return False
    
    source_file = backup_path / backup_file
    target_file = Path.cwd() / filename
    
    if not source_file.exists():
        print(f"备份文件 {backup_file} 不存在")
        return False
    
    print(f"准备恢复 {filename}:")
    print(f"  从备份: {backup_file}")
    print(f"  时间戳: {target_backup.get('timestamp')}")
    
    # 确认恢复
    response = input("是否确认恢复？(y/N): ").strip().lower()
    if response != 'y':
        print("恢复已取消")
        return False
    
    try:
        # 备份当前文件（如果存在）
        if target_file.exists():
            current_timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
            current_backup = target_file.parent / f"{filename}.before_restore.{current_timestamp}"
            shutil.copy2(target_file, current_backup)
            print(f"✓ 已备份当前 {filename} -> {current_backup.name}")
        
        # 恢复文件
        shutil.copy2(source_file, target_file)
        print(f"✅ 已恢复 {filename}")
        return True
        
    except Exception as e:
        print(f"❌ 恢复 {filename} 时出错: {e}")
        return False

if __name__ == "__main__":
    # 解析命令行参数
    if len(sys.argv) < 2:
        print("用法:")
        print("  python restore_config.py list                    # 列出所有备份")
        print("  python restore_config.py restore <index>         # 恢复指定备份")
        print("  python restore_config.py file <filename>         # 恢复特定文件")
        print("  python restore_config.py file <filename> <timestamp> # 从指定时间戳恢复文件")
        print("\n示例:")
        print("  python restore_config.py list")
        print("  python restore_config.py restore 1")
        print("  python restore_config.py file config.json")
        print("  python restore_config.py file mcp.json 20240101_120000")
        sys.exit(1)
    
    command = sys.argv[1]
    
    if command == "list":
        backups = list_backups()
        show_backup_list(backups)
    
    elif command == "restore":
        if len(sys.argv) < 3:
            print("错误: 请指定要恢复的备份索引")
            sys.exit(1)
        
        try:
            backup_index = int(sys.argv[2])
            restore_from_backup(backup_index)
        except ValueError:
            print("错误: 备份索引必须是数字")
            sys.exit(1)
    
    elif command == "file":
        if len(sys.argv) < 3:
            print("错误: 请指定要恢复的文件名")
            sys.exit(1)
        
        filename = sys.argv[2]
        timestamp = sys.argv[3] if len(sys.argv) > 3 else None
        
        restore_specific_file(filename, timestamp=timestamp)
    
    else:
        print(f"错误: 未知命令 '{command}'")
        sys.exit(1)