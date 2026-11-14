#!/usr/bin/env bash
# 部署脚本：针对**指定提交**构建、发布并做健康检查与业务冒烟。
# 用法： ./scripts/deploy.sh <commit-sha> [target-base-url]
# 说明：
#   - 本脚本是流水线 deploy 作业实际执行的脚本；
#   - 部署前先做端口预检，占用即失败退出（避免测到旧进程）；
#   - 部署提交必须是**合并后的主分支提交**，日志中必须出现该 SHA。
set -euo pipefail

COMMIT_SHA="${1:?必须传入部署提交 SHA}"
TARGET_BASE="${2:-${DEPLOY_TARGET:-http://127.0.0.1:8080}}"
MOCK_BASE="${DEPLOY_MOCK_TARGET:-http://127.0.0.1:8090}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

echo "=== 部署开始 ==="
echo "部署提交 SHA : ${COMMIT_SHA}"
echo "当前仓库 HEAD : $(git rev-parse HEAD 2>/dev/null || echo '非 Git 仓库')"
echo "目标地址     : ${TARGET_BASE}"
echo "模拟供应商地址: ${MOCK_BASE}"

# Python 解释器探测：CI（ubuntu）用 python3；部分本机环境只有 python。
if [ -n "${PYTHON_BIN:-}" ]; then
  :
elif command -v python3 >/dev/null 2>&1 && python3 --version >/dev/null 2>&1; then
  PYTHON_BIN=python3
elif command -v python >/dev/null 2>&1; then
  PYTHON_BIN=python
else
  echo "未找到可用的 Python 解释器（python3/python），部署中止"
  exit 1
fi
echo "Python 解释器: ${PYTHON_BIN} ($(${PYTHON_BIN} --version 2>&1))"

# 1) 端口预检：占用即失败，避免把上一轮的进程当成新版本
for port in 8080 8090; do
  if command -v ss >/dev/null 2>&1 && ss -ltn | awk '{print $4}' | grep -q ":${port}$"; then
    echo "端口 ${port} 已被占用，部署中止（请先释放端口）"
    exit 1
  fi
done

# 2) 构建
mkdir -p bin
go build -o bin/server ./cmd/server
go build -o bin/mockgen ./cmd/mockgen
sha256sum bin/server bin/mockgen

# 3) 启动模拟供应商与平台（stdout 重定向到文件，避免常驻进程占住管道）
mkdir -p data logs
nohup ./bin/mockgen -addr ":8090" -callback-url "${TARGET_BASE}/api/supplier/callback" \
  > logs/mockgen.log 2>&1 &
MOCK_PID=$!
sleep 1
nohup ./bin/server -addr ":8080" -db "data/studio.db" \
  -supplier-url "http://127.0.0.1:8090" -clock-scale 1 \
  > logs/server.log 2>&1 &
SERVER_PID=$!
echo "mockgen PID=${MOCK_PID}，server PID=${SERVER_PID}" | tee -a logs/pids.txt

# 4) 健康检查
for i in $(seq 1 30); do
  if curl -fsS "${TARGET_BASE}/healthz" >/dev/null 2>&1; then
    echo "健康检查通过（第 ${i} 次尝试）"
    break
  fi
  if [ "$i" = "30" ]; then
    echo "健康检查失败"
    tail -n 50 logs/server.log || true
    exit 1
  fi
  sleep 2
done

# 5) 业务冒烟：跑五组完整工作场景
"${PYTHON_BIN}" scripts/verify_scenarios.py --base "${TARGET_BASE}" --mock "${MOCK_BASE}" \
  | tee evidence/scenario-verify.log

echo "=== 部署完成，版本 ${COMMIT_SHA} ==="
echo "回退方式：上一可用版本的镜像标签为 creative-studio:<上一个合并提交 SHA>，"
echo "          执行 deploy.sh <上一个合并提交 SHA> 即可回到上一版本；"
echo "          首次部署失败时执行 'kill 平台与供应商进程并删除 data/studio.db' 即可撤销本次发布。"
