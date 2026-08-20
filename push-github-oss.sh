#!/bin/bash
set -euo pipefail
# 推送开源代码到GitHub（通过 index 过滤非开源路径，推送前断言验证）
# 过滤机制说明：
#   - sparse-checkout 只影响工作树，不影响提交内容，不能用于过滤提交
#   - 本脚本在临时分支上直接对 index 执行 git rm --cached 删除非开源路径，
#     提交后、推送前用 git ls-tree 允许列表校验确认提交中不含任何非 OSS 路径
# 用法: ./push-github-oss.sh "本次提交说明"
# 示例: ./push-github-oss.sh "fix: 修复SSE流式分片丢失问题"

COMMIT_MSG="$1"
if [ -z "$COMMIT_MSG" ]; then
  echo "用法: ./push-github-oss.sh \"提交说明\""
  echo "示例: ./push-github-oss.sh \"fix: 修复SSE流式分片丢失问题\""
  exit 1
fi

# 自动获取当前分支名，推送到同名远程分支
CURRENT_BRANCH=$(git branch --show-current)
if [ -z "$CURRENT_BRANCH" ]; then
  echo "错误: 无法获取当前分支名，请确保不在 detached HEAD 状态"
  exit 1
fi

# EXIT trap 统一清理：成功与失败路径（含 set -e 终止）都回切原分支并删除临时分支，
# 幂等无害——失败时仓库不再滞留在 oss-release-* 临时分支、被删路径不再残留为 untracked
cleanup() {
  git checkout -f "${CURRENT_BRANCH:-}" >/dev/null 2>&1 || true
  git branch -D "${TEMP_BRANCH:-}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

TIMESTAMP=$(date +%Y%m%d_%H%M%S)
TEMP_BRANCH="oss-release-$TIMESTAMP"

# 脏树守卫：工作区有未提交/未跟踪改动时中止，避免改动被静默遗漏出推送提交
if [ -n "$(git status --porcelain)" ]; then
  echo "错误: 工作区有未提交的改动，请先 commit 或 stash 后再发布"
  exit 1
fi

git checkout -b "$TEMP_BRANCH"

# OSS 发布包含的内容（列表之外的已跟踪文件会被从 index 删除后提交）：
#   pkg/core pkg/adapter pkg/plugin/interface.go pkg/plugin/oss
#   pkg/admin pkg/config cmd webui config.yaml go.mod go.sum
# 删除非开源路径（--cached 仅从 index 移除，不影响工作树）
git rm -r --cached --ignore-unmatch \
  pkg/plugin/enterprise \
  docs \
  .claude \
  Makefile \
  README.md \
  .gitignore \
  push-private.sh \
  push-github-oss.sh

git commit -m "[$TIMESTAMP] $COMMIT_MSG"

# 推送前断言（fail-closed 允许列表校验）：HEAD 树中任何不在 OSS 允许列表内的路径
# 都中止推送。允许列表即上方"OSS 发布包含的内容"列表；未来新增目录若未显式加入
# 允许列表即被拦截（enterprise/docs/Makefile/README 等由上方 git rm 从 index 移除）。
# 清理统一由 EXIT trap 执行，此处仅 exit 1
ALLOWED_PATHS='^(pkg/core/|pkg/adapter/|pkg/plugin/interface.go$|pkg/plugin/oss/|pkg/admin/|pkg/config/|cmd/|webui/|config.yaml$|go.mod$|go.sum$)'
leftover=$(git ls-tree -r --name-only HEAD | grep -Ev "$ALLOWED_PATHS" || true)
if [ -n "$leftover" ]; then
  echo "错误: 以下路径不在 OSS 允许列表中，已中止推送:"
  echo "$leftover"
  exit 1
fi

# 推送到GitHub同名分支（remote 地址待用户提供后配置）
git push git@github.com:druidcaesa/neuralgate.git "$TEMP_BRANCH":"$CURRENT_BRANCH" --force

# 清理由 EXIT trap 统一执行（回切原分支 + 删除 temp 分支），此处仅保留成功提示
echo "GitHub OSS repo pushed to branch [$CURRENT_BRANCH]: [$TIMESTAMP] $COMMIT_MSG (Enterprise code excluded)"
