# ClawHub 技能管理器使用示例

## 示例 1：搜索和安装文档处理技能

### 场景
用户需要处理各种文档格式（PDF、DOCX、TXT），想要安装一个文档处理技能。

### 步骤

1. **搜索相关技能**
   ```bash
   # 搜索文档处理相关技能
   clawhub search "文档处理"
   
   # 或者搜索特定类别
   clawhub search --category "document-processing"
   ```

2. **查看技能详情**
   ```bash
   # 假设找到了一个名为 "doc-processor" 的技能
   clawhub info doc-processor
   ```

3. **安装技能**
   ```bash
   # 安装最新版本
   clawhub install doc-processor
   
   # 或者安装特定版本
   clawhub install doc-processor@2.1.0
   ```

4. **配置技能**
   ```bash
   # 查看默认配置
   clawhub config doc-processor
   
   # 设置配置项
   clawhub config doc-processor --set \
     max_file_size=10MB \
     output_format=pdf \
     language=zh-CN
   ```

5. **验证安装**
   ```bash
   # 查看已安装技能列表
   clawhub list
   
   # 查看技能文档
   clawhub docs doc-processor
   ```

## 示例 2：管理多个技能

### 场景
用户已经安装了多个技能，需要定期维护和更新。

### 步骤

1. **查看所有已安装技能**
   ```bash
   # 简单列表
   clawhub list
   
   # 详细列表
   clawhub list --verbose
   ```

2. **检查更新**
   ```bash
   # 检查所有技能的可用更新
   clawhub update --check
   ```

3. **更新技能**
   ```bash
   # 更新所有技能
   clawhub update --all
   
   # 只更新特定技能
   clawhub update doc-processor image-editor
   ```

4. **卸载不需要的技能**
   ```bash
   # 卸载单个技能
   clawhub uninstall old-skill
   
   # 卸载多个技能
   clawhub uninstall skill1 skill2 skill3
   
   # 强制卸载（不提示确认）
   clawhub uninstall deprecated-skill --force
   ```

## 示例 3：团队协作配置管理

### 场景
团队需要共享相同的技能配置，确保所有成员使用相同的设置。

### 步骤

1. **导出当前配置**
   ```bash
   # 导出所有技能配置到文件
   clawhub config export > team-config.json
   ```

2. **分享配置文件**
   ```bash
   # 配置文件内容示例
   cat team-config.json
   ```

3. **导入配置**
   ```bash
   # 新成员导入团队配置
   clawhub config import < team-config.json
   ```

4. **验证配置同步**
   ```bash
   # 检查配置是否一致
   clawhub config doc-processor
   clawhub config image-editor
   ```

## 示例 4：故障排除

### 场景
技能安装后无法正常工作。

### 步骤

1. **检查技能状态**
   ```bash
   # 查看技能信息
   clawhub info problem-skill
   
   # 查看技能配置
   clawhub config problem-skill
   ```

2. **启用调试模式**
   ```bash
   # 使用调试模式运行技能
   clawhub --debug problem-skill --test
   ```

3. **检查依赖**
   ```bash
   # 查看技能依赖
   clawhub info problem-skill --dependencies
   ```

4. **重新安装**
   ```bash
   # 先卸载
   clawhub uninstall problem-skill
   
   # 清理缓存
   rm -rf ~/.clawhub/cache/skills/problem-skill
   
   # 重新安装
   clawhub install problem-skill
   ```

## 示例 5：账户管理

### 场景
用户需要管理 ClawHub 账户。

### 步骤

1. **登录账户**
   ```bash
   # 交互式登录
   clawhub login
   
   # 或者使用 API Key
   CLAWHUB_API_KEY=your-api-key clawhub whoami
   ```

2. **查看账户信息**
   ```bash
   # 查看当前登录用户
   clawhub whoami
   
   # 查看账户详情
   clawhub whoami --verbose
   ```

3. **登出账户**
   ```bash
   # 登出当前账户
   clawhub logout
   ```

4. **切换账户**
   ```bash
   # 先登出
   clawhub logout
   
   # 再登录新账户
   clawhub login
   ```

## 示例 6：批量操作

### 场景
用户需要对多个技能执行相同操作。

### 步骤

1. **批量安装**
   ```bash
   # 安装多个技能
   for skill in doc-processor image-editor data-analyzer; do
     clawhub install $skill
   done
   ```

2. **批量配置**
   ```bash
   # 为多个技能设置相同的 API Key
   for skill in openai-skill deepseek-skill; do
     clawhub config $skill --set api_key=your-shared-key
   done
   ```

3. **批量更新**
   ```bash
   # 更新所有技能，跳过失败的
   clawhub update --all --continue-on-error
   ```

4. **批量导出配置**
   ```bash
   # 导出特定类别的技能配置
   clawhub list --category "ai-tools" | while read skill; do
     clawhub config $skill > "${skill}-config.json"
   done
   ```

## 示例 7：技能开发工作流

### 场景
开发者创建了自己的技能，需要测试和发布。

### 步骤

1. **本地测试**
   ```bash
   # 从本地目录安装开发中的技能
   clawhub install ./my-skill
   ```

2. **测试配置**
   ```bash
   # 测试技能配置
   clawhub config my-skill --set test_mode=true
   ```

3. **发布到 GitHub**
   ```bash
   # 从 GitHub 安装测试
   clawhub install github.com/username/my-skill
   ```

4. **发布到 ClawHub 市场**
   ```bash
   # 发布技能（需要开发者权限）
   clawhub publish my-skill --version 1.0.0
   ```

## 最佳实践示例

### 1. 自动化脚本
```bash
#!/bin/bash
# auto-update-skills.sh

echo "开始自动更新技能..."
echo "时间: $(date)"

# 检查更新
echo "检查可用更新..."
clawhub update --check

# 更新所有技能
echo "更新所有技能..."
clawhub update --all

# 备份配置
echo "备份配置..."
clawhub config export > "backup-$(date +%Y%m%d).json"

echo "更新完成!"
```

### 2. 环境检查脚本
```bash
#!/bin/bash
# check-environment.sh

echo "=== ClawHub 环境检查 ==="

# 检查 CLI
if command -v clawhub &> /dev/null; then
    echo "✓ ClawHub CLI 已安装: $(clawhub --version)"
else
    echo "✗ ClawHub CLI 未安装"
    exit 1
fi

# 检查登录状态
if clawhub whoami &> /dev/null; then
    echo "✓ 已登录 ClawHub"
else
    echo "⚠ 未登录 ClawHub"
fi

# 检查已安装技能
echo "已安装技能:"
clawhub list --simple

echo "=== 检查完成 ==="
```

### 3. 配置验证脚本
```bash
#!/bin/bash
# validate-config.sh

SKILL_NAME=$1

if [ -z "$SKILL_NAME" ]; then
    echo "使用方法: $0 <技能名称>"
    exit 1
fi

echo "验证技能配置: $SKILL_NAME"

# 检查技能是否安装
if ! clawhub list | grep -q "$SKILL_NAME"; then
    echo "错误: 技能 '$SKILL_NAME' 未安装"
    exit 1
fi

# 检查配置
echo "当前配置:"
clawhub config "$SKILL_NAME"

# 检查必需配置项
REQUIRED_KEYS=("api_key" "endpoint")
for key in "${REQUIRED_KEYS[@]}"; do
    if ! clawhub config "$SKILL_NAME" | grep -q "$key"; then
        echo "警告: 缺少必需配置项 '$key'"
    fi
done

echo "配置验证完成"
```

这些示例展示了 ClawHub 技能管理器的各种使用场景，从基本的搜索安装到高级的批量操作和自动化脚本。