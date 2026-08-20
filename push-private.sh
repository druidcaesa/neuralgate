#!/bin/bash
set -euo pipefail
# 推送全量代码到私有仓库（含enterprise目录）
# 用法: ./push-private.sh "本次提交说明"
# 示例: ./push-private.sh "feat: 新增达梦存储适配器"

COMMIT_MSG="${1:-}"
if [ -z "$COMMIT_MSG" ]; then
  echo "用法: ./push-private.sh \"提交说明\""
  echo "示例: ./push-private.sh \"feat: 新增达梦存储适配器\""
  exit 1
fi

# 自动获取当前分支名
CURRENT_BRANCH=$(git branch --show-current)
if [ -z "$CURRENT_BRANCH" ]; then
  echo "错误: 无法获取当前分支名，请确保不在 detached HEAD 状态"
  exit 1
fi

TIMESTAMP=$(date +%Y%m%d_%H%M%S)
git add -A
git commit -m "[$TIMESTAMP] $COMMIT_MSG"
git push git@codeup.aliyun.com:661f776a552587f8fbe64a3c/neuralgate.git "$CURRENT_BRANCH"
echo "Private repo pushed to branch [$CURRENT_BRANCH]: [$TIMESTAMP] $COMMIT_MSG"