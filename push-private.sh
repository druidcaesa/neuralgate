#!/bin/bash
set -euo pipefail
# 推送全量代码到私有仓库（含enterprise目录）
# 提交内容由本地常规 git commit 承载，本脚本只负责推送：
#   无需参数；工作区有未提交改动时中止，避免绕过规范的提交流程

if [ -n "$(git status --porcelain)" ]; then
  echo "错误: 工作区有未提交的改动，请先按规范 commit 后再推送:"
  git status --short
  exit 1
fi

CURRENT_BRANCH=$(git branch --show-current)
if [ -z "$CURRENT_BRANCH" ]; then
  echo "错误: 无法获取当前分支名，请确保不在 detached HEAD 状态"
  exit 1
fi

REMOTE_URL="git@codeup.aliyun.com:661f776a552587f8fbe64a3c/neuralgate.git"
git push "${REMOTE_URL}" "${CURRENT_BRANCH}"
echo "✅ Private repo pushed to branch [$CURRENT_BRANCH]"
