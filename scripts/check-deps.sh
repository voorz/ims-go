#!/bin/bash
# check-deps.sh — ims-go 发布前依赖链检查（对标 check-deps.ps1）
# 检查：
#   1. 本地分支必须 main
#   2. 工作树干净
#   3. 本地 = 远程（已 push）
#   4. go.mod 依赖版本均为最新 tag（sipgo 等）
set -e

REPO=~/workspace/user/projects/vohive/ims-go
cd "$REPO"

ALL_OK=true

echo "========================================"
echo " ims-go 发布前检查"
echo "========================================"

# 1. 分支
BRANCH=$(git rev-parse --abbrev-ref HEAD)
echo "Branch: $BRANCH"
if [ "$BRANCH" != "main" ]; then ALL_OK=false; echo "  ❌ 分支不是 main"; fi

# 2. 工作树
if [ -n "$(git status --porcelain)" ]; then
  ALL_OK=false; echo "  ❌ 工作树不干净"
else
  echo "WorkingTree: Clean"
fi

# 3. 本地=远程
LOCAL=$(git rev-parse HEAD)
REMOTE=$(git rev-parse origin/main 2>/dev/null || echo "none")
if [ "$LOCAL" = "$REMOTE" ]; then
  echo "Pushed: Yes"
else
  ALL_OK=false; echo "  ❌ 本地 != 远程（未 push）"
fi

# 4. 关键依赖版本检查
echo ""
echo "--- go.mod 关键依赖 ---"
for mod in "github.com/emiago/sipgo" "github.com/icholy/digest"; do
  ver=$(grep -E "^\s*$mod\s+" go.mod | awk '{print $2}' || echo "-")
  echo "  $mod: $ver"
done

echo ""
echo "========================================"
if $ALL_OK; then
  echo " ALL CHECKS PASSED"
else
  echo " CHECKS FAILED"
  exit 1
fi
echo "========================================"
