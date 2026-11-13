# AGENTS.md 读取与使用记录

> 本表登记**每一次实际读取**项目级 `AGENTS.md` 的行为。
> 题面共同要求 §3：必须提交约束文件的**历史版本、实际加载或读取记录及对应任务**，
> 说明哪些行为受到约束、是否调整过约束、为什么调整。
> 版本快照：`docs/agents/AGENTS-v1.0.md`（首版，原文与根目录 `AGENTS.md` 的 v1.0 内容一致）。

## 一、读取记录

| 序号 | 时间（Asia/Shanghai） | 读取者 | 读取版本 | 触发场景 / 对应任务 | 读取后受到约束的具体行为 | 对应产物 |
|---|---|---|---|---|---|---|
| 1 | 2026-11-13 21:34 | 开发 Agent（主会话） | v1.0 | 阶段一：需求分析落盘后、编写第一行业务代码之前（T-01 约束准备） | 按 §4 表格先固化 R1–R8 的状态矩阵，再决定实体与状态枚举；按 §5 确定三类主体的权限边界；按 §2 锁定 SQLite + 标准库 net/http 的选型 | `docs/01-需求分析.md`、`docs/_question-06.md` |
| 2 | 2026-11-13 21:36 | 开发 Agent（主会话） | v1.0 | 任务规划阶段（T-02），拆分实现/测试/验收/部署任务 | 按 §7 把「覆盖率整体与各核心包 ≥85%」「并发可重复验证」「竞态检测」写成任务的完成条件，而不是最后补测 | `docs/03-任务规划.md` |
| 3 | 2026-11-13 21:40 | 开发 Agent（主会话） | v1.0 | Skill 设计阶段（T-03），编写两个自编 Skill | 按 §9 规定 Skill 只能输出「通过/不通过/待补证」，判定逻辑必须写在脚本里，不得为了好看放宽阈值 | `skills/material-guard/SKILL.md`、`skills/acceptance-audit/SKILL.md` |
| 4 | 2026-11-13 21:45 | 开发 Agent（主会话） | v1.0 | 编码阶段（T-04～T-08）：domain/store/service/httpapi/cmd | 按 §4 状态矩阵实现 `CanStartGeneration`/`CanReview`/`CanAdopt`/`CanWithdraw`；按 §5 只从会话令牌取身份；按 §6 统一错误哨兵与状态码映射 | `internal/domain/`、`internal/store/`、`internal/service/`、`internal/httpapi/` |
| 5 | 2026-11-13 22:05 | 开发 Agent（主会话） | v1.0 | 自检阶段（T-09），第一次运行 `material-guard` | 按 §9「必须真实调用并把原始输出存入 evidence/」，把失败输出也留档；按 §7 第 7 条区分「修判据」与「弱化断言」 | `evidence/material-guard.md`（首轮 FAIL 记录） |
| 6 | 2026-11-13 22:20 | 测试 Agent（独立上下文） | v1.0 | 独立测试阶段（T-10）：读取题面与对应版本代码，制定并执行测试 | 按 §12「不得采信『已完成』自述」；按 §7 第 8 条使用未污染数据库验证撤销与次数上限 | `evidence/independent-test-report.md` |
| 7 | 2026-11-13 22:40 | 验收 Agent（独立上下文） | v1.0 | 独立验收阶段（T-11）：依据原始题面逐项验证最终版本 | 按 §13 版本历史核对约束与实现是否一致；按 §6 第 5 条检查 500 响应是否透出内部细节 | `evidence/independent-acceptance-report.md` |
| 8 | 2026-11-13 23:00 | 部署 Agent（主会话执行） | v1.0 | 合并与自动部署阶段（T-12） | 按 §10 第 5 条以**合并提交 SHA** 重跑门禁与部署；按 §11 第 4 条保证检查项名与流水线作业名逐字一致 | `evidence/deployment-main.log`、`.github/workflows/ci.yml` |

## 二、哪些行为受到约束

| 约束条款 | 实际生效的行为 | 证据 |
|---|---|---|
| §1 第 5 条（不得只交异步任务轮询接口） | 除回调处理外，实现了需求创建、复核、采用、撤销、核查与最小页面 | `internal/httpapi/`、`docs/02-原型设计.md` |
| §4 R2（至多一个处理中任务 / 请求编号幂等） | 数据库层用部分唯一索引兜底，服务层用事务 + 前置判定双重保护 | `internal/store/schema.sql`、`internal/service/requirement.go` |
| §4 R6 / R7（历史候选、撤销不复活） | 候选带 `is_current` 标记；撤销后的迟到成功与迟到失败都只留审计 | `internal/service/callback.go`、`internal/service/requirement.go` |
| §5（身份只来自会话令牌） | 所有业务接口经 `withUser` 中间件，越权返回 403 | `internal/httpapi/server.go` |
| §7 第 2 条（覆盖率剖面绝对路径、先删旧文件） | `scripts/coverage_gate.py` 先删除旧剖面并用绝对路径产出 | `scripts/coverage_gate.py` |
| §8 第 4 条（泄密扫描不得使用白名单） | 泄密扫描脚本无白名单、无例外目录，文档与代码表述一致 | `skills/acceptance-audit/scripts/acceptance_audit.py` |
| §9（Skill 不得放宽判定标准） | `material-guard` 首轮 FAIL 判据有缺陷时，**修判据**并留痕，而不是降低阈值 | `evidence/material-guard.md` 首轮与复检记录 |

## 三、约束是否被调整过

| 版本 | 变更内容 | 变更原因 | 落盘时间 | 是否晚于对应代码 |
|---|---|---|---|---|
| v1.0 | 首版，全部条款一次性固化 | 需求分析阶段确认 A1–A7 后、编码前必须可执行 | 2026-11-13 21:40（早于首行业务代码） | 否 |

**如实登记**：本题的 `AGENTS.md` v1.0 在**第一行业务代码之前**落盘并被实际读取，
不存在「事后补写」的情况；本轮**未**修改约束条款，因此没有「约束晚于代码」的偏差需要登记。
若后续因整改需要升版，必须在根目录 `AGENTS.md` 的「版本历史」中写明原因，并新增 `docs/agents/AGENTS-vX.Y.md` 快照。
