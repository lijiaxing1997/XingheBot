#!/bin/bash

# ClawHub CLI 基本功能测试脚本
# 这个脚本用于测试 ClawHub 管理技能的基本功能

set -e

echo "=== ClawHub 技能管理器测试脚本 ==="
echo "开始时间: $(date)"
echo ""

# 检查 ClawHub CLI 是否安装
check_clawhub_installed() {
    echo "1. 检查 ClawHub CLI 是否安装..."
    if command -v clawhub &> /dev/null; then
        echo "   ✓ ClawHub CLI 已安装"
        echo "   版本: $(clawhub --version 2>/dev/null || echo '未知')"
    else
        echo "   ✗ ClawHub CLI 未安装"
        echo "   请先安装 ClawHub CLI:"
        echo "   npm install -g @clawhub/cli"
        return 1
    fi
    echo ""
}

# 检查账户状态
check_account_status() {
    echo "2. 检查 ClawHub 账户状态..."
    if clawhub whoami &> /dev/null; then
        echo "   ✓ 已登录 ClawHub 账户"
    else
        echo "   ⚠ 未登录或登录已过期"
        echo "   请运行: clawhub login"
    fi
    echo ""
}

# 测试搜索功能（模拟）
test_search_function() {
    echo "3. 测试搜索功能..."
    echo "   搜索命令示例:"
    echo "   - clawhub search \"文档处理\""
    echo "   - clawhub search --popular"
    echo "   - clawhub search --recent"
    echo "   - clawhub search --category \"ai-assistant\""
    echo ""
}

# 测试列表功能
test_list_function() {
    echo "4. 测试已安装技能列表..."
    echo "   列表命令示例:"
    echo "   - clawhub list"
    echo "   - clawhub list --verbose"
    echo "   - clawhub list --category \"productivity\""
    echo ""
}

# 测试安装功能
test_install_function() {
    echo "5. 测试安装功能..."
    echo "   安装命令示例:"
    echo "   - clawhub install skill-name"
    echo "   - clawhub install skill-name@1.0.0"
    echo "   - clawhub install github.com/username/repo"
    echo ""
}

# 测试配置功能
test_config_function() {
    echo "6. 测试配置功能..."
    echo "   配置命令示例:"
    echo "   - clawhub config skill-name"
    echo "   - clawhub config skill-name --set api_key=your-key"
    echo "   - clawhub config skill-name --set endpoint=https://api.example.com"
    echo "   - clawhub config skill-name --unset api_key"
    echo ""
}

# 测试更新功能
test_update_function() {
    echo "7. 测试更新功能..."
    echo "   更新命令示例:"
    echo "   - clawhub update --check"
    echo "   - clawhub update --all"
    echo "   - clawhub update skill-name"
    echo ""
}

# 测试卸载功能
test_uninstall_function() {
    echo "8. 测试卸载功能..."
    echo "   卸载命令示例:"
    echo "   - clawhub uninstall skill-name"
    echo "   - clawhub uninstall skill1 skill2 skill3"
    echo "   - clawhub uninstall skill-name --force"
    echo ""
}

# 显示使用建议
show_usage_tips() {
    echo "=== 使用建议 ==="
    echo ""
    echo "1. 首次使用:"
    echo "   clawhub login          # 登录账户"
    echo "   clawhub search         # 搜索技能"
    echo "   clawhub install        # 安装技能"
    echo ""
    echo "2. 日常维护:"
    echo "   clawhub list           # 查看已安装技能"
    echo "   clawhub update --check # 检查更新"
    echo "   clawhub update --all   # 更新所有技能"
    echo ""
    echo "3. 故障排除:"
    echo "   clawhub --debug        # 启用调试模式"
    echo "   clawhub --verbose      # 显示详细输出"
    echo "   clawhub --help         # 查看帮助"
    echo ""
}

# 运行所有测试
main() {
    echo "开始 ClawHub 技能管理器测试..."
    echo ""
    
    check_clawhub_installed || exit 1
    check_account_status
    test_search_function
    test_list_function
    test_install_function
    test_config_function
    test_update_function
    test_uninstall_function
    show_usage_tips
    
    echo "=== 测试完成 ==="
    echo "结束时间: $(date)"
    echo ""
    echo "提示: 以上是命令示例，实际使用时请替换相应的参数值。"
}

# 执行主函数
main "$@"