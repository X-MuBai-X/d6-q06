---
name: material-guard
description: 检查营销素材 AI 生图需求的状态与版本不变量（当前候选唯一、生成次数上限、撤销不复活、描述快照不可覆盖等），用于每次改动业务逻辑后与提交前自检。
---

# material-guard

## 1. 用途

回答：**「改了生成／回调／复核／撤销相关逻辑之后，题面 R1–R8 的状态与版本不变量还是不是原来那套？」**

由三部分组成：

1. **数据库不变量静态检查**（`scripts/material_guard.py`，直接只读 SQLite 库，逐条判定 R1–R8）；
2. **业务口径与源码一致性检查**（脚本读取 `internal/domain/rules.go` 与 `internal/service/`，核对关键常量与状态判定）；
3. **覆盖率统计**（复用 `scripts/coverage_gate.py`，唯一统计口径）。

本 Skill 只报告「通过／不通过／待补证」，**不允许放宽判定标准**。

## 2. 触发场景

- 改动了 `internal/domain/rules.go`、`internal/service/`、`internal/store/` 中与需求状态、生成任务、候选、回调、撤销相关的逻辑之后；
- 提交前自检；
- 独立测试 Agent 发现状态相关缺陷后，改完代码复检；
- 交付前做一次全量体检。

## 3. 输入

| 参数 | 说明 | 默认 |
|---|---|---|
| `--repo` | 仓库根目录 | 自动向上查找（当前工作目录） |
| `--db` | 待检查的 SQLite 数据库文件 | `<repo>/data/studio.db` |
| `--min-coverage` | 核心包覆盖率下限 | 85 |
| `--out` | Markdown 报告输出路径 | `<repo>/evidence/material-guard.md` |
| `--skip-tests` | 只做静态不变量与源码一致性检查 | false |

## 4. 执行步骤

1. 定位仓库与数据库文件；数据库不存在时直接判「待补证」，**不得**输出通过；
2. 执行数据库不变量检查（对应题面规则编号）：
   - **G1（R1）** 每个成功任务都保存了非空描述快照，且同一需求的历史任务快照互不相同；
   - **G2（R2）** 同一需求至多有一个 `processing` 任务；同一 `request_no` 至多对应一个任务；
   - **G3（R3）** 单需求生成次数 ≤ 3；`rejected`／`failed`／`timeout` 任务不得产出候选；
   - **G4（R4）** 每个 `succeeded` 任务恰好一个候选；同一需求至多一个 `is_current=1` 的候选；
   - **G5（R5）** `delivered` 需求必须存在 `approved` 状态的当前候选；
   - **G6（R6）** 非当前候选一律不得处于「待复核」状态（发起下一次生成后旧候选只作历史）；
   - **G7（R7）** `withdrawn` 需求不得存在候选，且不得存在 `delivered` 的处置记录；
   - **G8（R8）** 每个需求必须存在 `requirement_created` 事件，且每个生成任务至少有一条相关审计事件；
   - **G9（R7）** **撤销后不得复活**：已撤销（存在 `requirement_withdrawn` 事件）的需求，
     状态不得被改写为 `generating`／`failed`／`exhausted`；撤销事件之后不得再出现
     `generation_started`／`generation_timeout`／`generation_failed`／`generation_rejected`／
     `candidate_created`／`candidate_reviewed`／`requirement_adopted` 等把需求往前推的事件；
3. 执行源码一致性检查：核对 `domain.MaxAttempts == 3`、`domain.GenerationTimeout == 60s`、
   `domain.CanStartGeneration` / `CanReview` / `CanAdopt` / `CanWithdraw` 的允许状态集合与题面一致，
   **并且核对业务层是否真的调用了判定**（只看声明不够）：
   - `S3-CallCanStartGeneration`：`internal/service/` 下的非测试文件中存在 `CanStartGeneration(` 调用点；
   - `S3-CallInStartGeneration`：`internal/service/requirement.go` 的 `StartGeneration` 函数体内调用了
     `CanStartGeneration`（否则撤销过的需求仍可被再次发起，对应题面 R7）；
   两项判据均按**语义**（是否存在调用点）判定，不依赖具体行号；
4. 执行覆盖率门禁（`scripts/coverage_gate.py`），整体与各核心包均须 ≥ `--min-coverage`；
5. 汇总判定并落盘报告。

## 5. 输出格式

Markdown 报告（默认 `evidence/material-guard.md`）：

```
# material-guard 检查报告
- 判定：PASS / FAIL / 待补证
- 数据库：<绝对路径>
- 覆盖率：整体 xx.xx%；各核心包 …

## 一、数据库不变量
| 编号 | 规则 | 检查项 | 结论 | 证据 |
| G1 | R1 | 每个成功任务都有描述快照 | PASS | tasks.attempt1.description_snapshot 非空 |

## 二、源码一致性
| 编号 | 检查项 | 结论 | 证据（文件:行号） |

## 三、覆盖率
| 包 | 覆盖率 | 结论 |

## 四、失败项与建议
（FAIL 时逐条列出违反的记录与建议修法）
```

同时输出结构化 JSON（与 Markdown 同目录，`<name>.json`），字段：
`status`、`db`、`checks[]`（含 `id`／`rule`／`name`／`status`／`evidence`）、`coverage`、`generated_at`。

## 6. 失败处理

- 任一数据库不变量不通过 → 整体 **FAIL**，报告里必须给出**违反的记录 ID 与字段值**；
- 覆盖率低于阈值 → **FAIL**；
- 覆盖率门禁**未执行**（`--skip-tests` 或脚本缺失，状态为「待补证」）→ 整体判定为 **待补证**，
  **不得**判为 PASS，并在报告中写明原因与补跑方式；
- 数据库文件不存在、Python 环境缺失或库不可读 → **待补证**，写明原因，**不得**输出 PASS；
- 检查脚本自身判据有缺陷时，**修判据并在报告里写明修改原因**，不得为了让结论好看而降低阈值；
- **禁止**通过删除失败用例、弱化断言或排除生产代码来让检查通过。

## 7. 完成标准

- G1–G9 九项数据库不变量全部有结论（pass 或 fail）；
- 源码一致性检查全部有结论并给出 `文件:行号`；
- 覆盖率门禁已执行并给出结论；
- 报告已落盘到 `--out` 指定路径；
- 存在 FAIL 时报告首行结论必须是 FAIL；覆盖率门禁为「待补证」时首行结论必须是待补证。
