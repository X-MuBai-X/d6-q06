## 题目 06 营销素材的 AI 生图、重做与确认交付

### 1. 关联工作项

- 工作项编号：**`#TBD`（未核验）**。本机未安装 `meegle` CLI，无法执行
  `meegle workitem get --work-item-id <数字编号> --project-key r17te9` 完成编号核验。
  按题面共同要求第 9 节「编号未经有效核验的提交不计为合规提交」，本次 `feat`/`fix` 提交
  保留 `#TBD` 占位并如实登记偏差，**不伪造编号、不声称查询成功**。待教师提供可用工作项编号后回填。

### 2. 变更内容

- R1–R8 完整业务闭环：需求创建 → 生成（幂等、描述快照） → 供应商回调（验签、幂等、异常可查）
  → 复核（不通过须写原因） → 需求人确认采用 / 重做 → 撤销。
- 分层实现：`internal/domain`（规则与状态机）、`internal/store`（SQLite 持久化与事务）、
  `internal/service`（业务编排）、`internal/supplier`（HMAC 回调签名）、`internal/httpapi`
  （JSON 接口 + 4 个业务页面）、`cmd/server`、`cmd/mockgen`（模拟供应商）。
- 四个自编产物：`AGENTS.md`（本题约束 + 版本快照 + 读取记录）、`skills/material-guard`、
  `skills/acceptance-audit`、`scripts/coverage_gate.py`（唯一覆盖率口径）。
- 流水线与部署：`.github/workflows/ci.yml`（质量检查 → 覆盖率门禁 → 构建 → 部署 → 健康检查 → 业务冒烟）、
  `Dockerfile`、`scripts/deploy.sh`。
- 文档与证据：`docs/01`–`docs/14`、`evidence/`（覆盖率原件、场景日志、独立测试与验收报告、缺陷修复闭环、Git 报文）。

### 3. 测试与验收情况

- `gofmt -l .` 零差异；`go vet ./...` 无告警；`go test ./... -count=1` 七包全绿（原始输出 `evidence/go-test-output.txt`）。
- 覆盖率（唯一口径 `scripts/coverage_gate.py`）：整体 **86.05%**，各核心包
  domain 89.71% / store 86.80% / service 86.76% / httpapi 89.51% / supplier 87.10%，**均 ≥85%**。
- 五组工作场景实测：`scripts/verify_scenarios.py` 在题面 60 秒口径下 **36 项断言全部通过**
  （`evidence/scenario-verify.log`）。
- 自编 Skill 判定：`material-guard` **PASS**；`acceptance-audit` **FAIL 0 项 / 通过 23 项 / 仅部署项待补证**。
- 独立测试 Agent（全新上下文）检出 9 条缺陷（含 2 条严重），**已全部修复并复检**，闭环记录见
  `evidence/defect-fix-round1*.md`；独立验收 Agent 报告见 `evidence/independent-acceptance-report.md`。

### 4. 风险与影响面

- 数据库为 SQLite 单文件（`modernc.org/sqlite` 纯 Go 驱动），无外部中间件依赖。
- 教学时钟倍率 `-clock-scale` 只改时限不改规则（默认 1 = 题面 60 秒；演示可用 0.1）。
- 回调验签依赖共享密钥，仅在演示环境使用；真实环境需密钥管理与轮转。
- `go test -race` 在本机因缺少 C 编译器未执行，已在 CI 中配置，待流水线运行补证。

### 5. 回滚方式

- 合并前：本 PR 未合入 `main`，关闭即回滚。
- 合并后：回到上一可用版本执行 `./scripts/deploy.sh <上一合并提交 SHA>`；
  首次部署失败时按 `evidence/deployment-main.log` 记录的撤销步骤执行（停止进程并清理本次数据文件）。

### 6. 关联证据

`evidence/` 全目录，重点：`coverage-summary.txt`、`scenario-verify.log`、`go-test-output.txt`、
`independent-test-report.md`、`independent-acceptance-report.md`、`defect-fix-round1*.md`、
`acceptance-audit/`、`material-guard.md`、`github-mcp/`、`deployment-main.log`、`token-ledger.json`。
