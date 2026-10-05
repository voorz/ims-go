#!/usr/bin/env bash
# ims-go 机器门禁（P5：规范即门禁）。CI 必跑，本地：./scripts/gates.sh
# 任一门禁失败即整体失败；失败必须在本批次内修复，不得堆积。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PASS=0
FAIL=0

gate() { # gate <编号> <名称> <命令...>
  local id="$1" name="$2"; shift 2
  if "$@" > /tmp/gate-out.txt 2>&1; then
    echo "门禁${id} ✅ ${name}"
    PASS=$((PASS+1))
  else
    echo "门禁${id} ❌ ${name}"
    sed 's/^/    /' /tmp/gate-out.txt
    FAIL=$((FAIL+1))
  fi
}

SRC_DIRS="ims internal"

# ① 禁 raw string 拼 SIP（红线#1）：非测试代码中禁 Sprintf 拼 SIP 起始行/版本行
gate1() {
  local hits
  hits=$(grep -rn --include='*.go' \
    -e 'Sprintf\s*(\s*"\(SIP/2\.0\|INVITE \|REGISTER \|MESSAGE \|BYE \|CANCEL \|ACK \|OPTIONS \|SUBSCRIBE \|NOTIFY \|PRACK \|UPDATE \|REFER \|INFO \)' \
    -e '"SIP/2\.0"' \
    $SRC_DIRS 2>/dev/null | grep -v '_test\.go:' || true)
  if [ -n "$hits" ]; then echo "$hits"; return 1; fi
}

# ② 公开 API（ims/）禁 map[string]interface{} 与裸 interface{}（红线#4/#6）
gate2() {
  local hits
  hits=$(grep -rn --include='*.go' -e 'map\[string\]interface{}' -e 'interface{}' \
    ims 2>/dev/null | grep -v '_test\.go:' || true)
  if [ -n "$hits" ]; then echo "$hits"; return 1; fi
}

# ③ 导出类型定义只允许出现在 types.go / runtime_types.go（红线#9/P4）
# 仅适用于新写的包；收拢的第三方代码（internal/swu/*、internal/netplane/*、
# internal/dns）保持原结构以便与上游对应，不强制重组。
gate3() {
  local hits=""
  while IFS= read -r f; do
    case "$f" in
      */types.go|*/runtime_types.go|*_test.go|*/doc.go) continue ;;
    esac
    local m
    m=$(grep -n '^type [A-Z]' "$f" || true)
    if [ -n "$m" ]; then hits+="${f}:\n${m}\n"; fi
  done < <(find ims internal/sim -name '*.go')
  if [ -n "$hits" ]; then printf '%b' "$hits"; return 1; fi
}

# ④ 除 ims/ 外不得存在其他顶层可公开 import 的包（D-010 单公开包）
gate4() {
  local bad=""
  for d in */; do
    case "$d" in
      ims/|internal/|scripts/|".github/") continue ;;
    esac
    if ls "$d"*.go >/dev/null 2>&1; then bad+="${d}\n"; fi
  done
  if [ -n "$bad" ]; then printf 'unexpected top-level packages:\n%b' "$bad"; return 1; fi
}

# ⑤ 禁新增 ToInternal/FromInternal/adapt*（D-007 单 Config 零转换）
gate5() {
  local hits
  hits=$(grep -rn --include='*.go' -e 'ToInternal' -e 'FromInternal' -e 'adapt[A-Z]' \
    $SRC_DIRS 2>/dev/null | grep -v '_test\.go:' || true)
  if [ -n "$hits" ]; then echo "$hits"; return 1; fi
}

# ⑥ gofmt + go vet 全绿
gate6() {
  export PATH="$HOME/go/go/bin:$PATH"
  local unformatted
  unformatted=$(gofmt -l $SRC_DIRS 2>/dev/null || true)
  if [ -n "$unformatted" ]; then echo "gofmt -l:"; echo "$unformatted"; return 1; fi
  go vet ./... 2>&1
}

gate "①" "禁 raw string 拼 SIP" gate1
gate "②" "公开 API 禁 map/interface{}" gate2
gate "③" "导出类型集中 types.go" gate3
gate "④" "单公开包 ims" gate4
gate "⑤" "禁 ToInternal/FromInternal" gate5
gate "⑥" "gofmt + go vet" gate6

echo "----"
echo "通过 ${PASS} / 失败 ${FAIL}"
[ "$FAIL" -eq 0 ]
