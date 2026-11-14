#!/usr/bin/env bash
# 质量门禁脚本：gofmt + go vet + 单元测试 + 覆盖率门禁（CI 与本机共用同一口径）。
# 用法： ./scripts/quality_gate.sh
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

echo "=== 1/4 gofmt ==="
FMT_OUT="$(gofmt -l .)"
if [ -n "$FMT_OUT" ]; then
  echo "gofmt 存在差异："
  echo "$FMT_OUT"
  exit 1
fi
echo "gofmt 零差异"

echo "=== 2/4 go vet ==="
go vet ./...
echo "go vet 无告警"

echo "=== 3/4 单元测试 ==="
go test ./... -count=1

echo "=== 4/4 覆盖率门禁 ==="
python3 scripts/coverage_gate.py --root . --out-dir evidence --min 85

echo "=== 质量门禁全部通过 ==="
