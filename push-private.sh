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

CURRENT_BRANCH=$(git branch --show-current)
if [ -z "$CURRENT_BRANCH" ]; then
  echo "错误: 无法获取当前分支名，请确保不在 detached HEAD 状态"
  exit 1
fi

# 检查是否有待提交变更
if [ -n "$(git status --porcelain)" ]; then
  TIMESTAMP=$(date +%Y%m%d_%H%M%S)
  git add -A
  git commit -m "[$TIMESTAMP] $COMMIT_MSG"
  echo "✅ 已生成本地提交"
else
  echo "ℹ️ 工作区已经干净，跳过commit，直接推送现有本地分支"
fi

REMOTE_URL="git@codeup.aliyun.com:661f776a552587f8fbe64a3c/neuralgate.git"
git push "${REMOTE_URL}" "${CURRENT_BRANCH}"
echo "✅ Private repo pushed to branch [$CURRENT_BRANCH]"