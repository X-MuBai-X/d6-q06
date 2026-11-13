# d6-q06 · 题目 06 营销素材的 AI 生图、重做与确认交付

内容团队使用 AI 图片服务为营销活动制作配图：业务人员填写素材需求，平台提交供应商生成，
设计人员查看候选结果，业务人员确认采用或要求重做，运营负责核查长期未完成和异常生成任务。

本仓库是《Go项目业务场景题》题目 06 的**完整交付**：业务实现、需求与原型、任务计划、
项目级 `AGENTS.md`、两个自编 Skill、测试与覆盖率证据、CI/CD 配置与会话记录。

## 快速开始

```bash
# 1) 启动模拟生成供应商（可控触发成功/失败/拒绝/超时/重复投递）
go run ./cmd/mockgen -addr :8090 -callback-url http://127.0.0.1:8080/api/supplier/callback

# 2) 启动平台（另开一个终端）
go run ./cmd/server -addr :8080 -db data/studio.db -supplier-url http://127.0.0.1:8090 -clock-scale 1

# 3) 浏览器打开 http://127.0.0.1:8080/login
#    预置账号：demand-a / demand-b（需求人）、review-a（设计复核人）、ops-a（运营）
#    演示口令见《docs/04-部署与使用说明.md》（文档中以掩码展示）
```

质量门禁与场景验证：

```bash
./scripts/quality_gate.sh                       # gofmt + go vet + 单测 + 覆盖率门禁
python3 scripts/verify_scenarios.py             # 五组完整工作场景端到端验证
python3 skills/material-guard/scripts/material_guard.py --repo . --db data/studio.db
python3 skills/acceptance-audit/scripts/acceptance_audit.py --repo .
```

## 目录

| 路径 | 说明 |
|---|---|
| `cmd/server` | 平台服务入口 |
| `cmd/mockgen` | 模拟生成供应商 |
| `internal/domain` | 实体、状态枚举与规则判定 |
| `internal/store` | SQLite 持久化与事务 |
| `internal/service` | 业务编排（生成/回调/复核/采用/撤销/巡检/查询） |
| `internal/httpapi` | JSON 接口与可操作页面 |
| `scripts` | 质量门禁、覆盖率门禁、场景验证、部署脚本 |
| `skills` | 自编 Skill：material-guard、acceptance-audit |
| `docs` | 需求分析、原型、任务规划、部署说明、会话导出、AI 使用说明、交付说明、讲解目录 |
| `evidence` | 真实执行证据（覆盖率、场景验证、部署日志、MCP 建仓报文、验收报告） |

## 文档索引

- 需求与设计：`docs/01-需求分析.md`、`docs/02-原型设计.md`、`docs/03-任务规划.md`
- 运行与部署：`docs/04-部署与使用说明.md`
- Agent 约束：`AGENTS.md`、`docs/agents/`
- 测试与验收：`evidence/`
- AI 使用：`docs/06-会话导出.md`、`docs/09-AI使用说明.md`、`skill-usage.md`
- 交付与讲解：`docs/10-交付与发布说明.md`、`docs/11-班级群提交消息.md`、`docs/12-讲解视频带时间点目录.md`
