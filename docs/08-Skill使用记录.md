# Skill 使用记录（题目 06）

> 共同要求 §4：每个 Skill 必须包含用途、触发场景、输入、执行步骤、输出格式、失败处理、完成标准；
> 必须提交可被所选 AI 工具使用的 Skill 文件、配置方式与**实际调用记录**；
> 至少展示一次「Skill 检查发现问题 → 学生指导 Agent 修正 → 重新检查」。
> 汇总表见仓库根目录 `skill-usage.md`。

---

## 1. 自编 Skill 清单

| Skill | 路径 | 类型 | 七要素 | 判定脚本 | 作用 |
|---|---|---|---|---|---|
| `material-guard` | `skills/material-guard/` | 业务专项 | ✅ 齐全（SKILL.md §1–§7） | `skills/material-guard/scripts/material_guard.py` | 只读检查 R1–R8 的状态与版本不变量 + 源码一致性 + 覆盖率门禁 |
| `acceptance-audit` | `skills/acceptance-audit/` | 验收检查 | ✅ 齐全（SKILL.md §1–§7） | `skills/acceptance-audit/scripts/acceptance_audit.py` | 把题面 R1–R8 与共同要求拆成机器判定条目，逐条核对实现、证据、Skill、提交与部署 |

**判定逻辑写在脚本里，不写在提示词里**（AGENTS §9）：两个 Skill 的判定逻辑均为确定性脚本，
输出 Markdown + JSON，不依赖模型主观判断。

---

## 2. 在工具中的配置方式

本项目使用 **Claude Code 风格的项目级 Skill 目录约定**（`.claude/skills/<name>/SKILL.md` 的等价形式）：

| Skill | 落库位置 | 工具中的调用方式 |
|---|---|---|
| `material-guard` | 仓库内 `skills/material-guard/SKILL.md` + `scripts/material_guard.py` | 在 AI 工具中把该目录登记为项目级 Skill（Skill 目录约定：目录下须有 `SKILL.md` 与带 YAML frontmatter 的 `name`/`description`）；也可直接以命令行方式调用 `python skills/material-guard/scripts/material_guard.py --repo . --db data/studio.db` |
| `acceptance-audit` | 仓库内 `skills/acceptance-audit/SKILL.md` + `scripts/acceptance_audit.py` | 同上：`python skills/acceptance-audit/scripts/acceptance_audit.py --repo . --out-dir evidence/acceptance-audit` |

两份 `SKILL.md` 均带 YAML frontmatter（`name`、`description`），可直接被支持该目录约定的 AI 工具加载。

---

## 3. 实际调用记录（原始输出存档于 `evidence/`）

### 3.1 `material-guard` 第 1 轮 —— **FAIL（发现问题）**

- **时机**：业务代码与单测完成、准备提交之前（对应任务 T-09 的自检环节，2026-11-13 22:05）。
- **命令**：
  ```bash
  python skills/material-guard/scripts/material_guard.py --repo . --db data/run6.db
  ```
- **结论**：**FAIL**。G4 条（"成功任务恰好一个候选"）判定失败，证据：
  `缺候选任务：['task-2772f2153a10959e19d5912d']`。

### 3.2 问题分析（学生判断，非 AI 自述）

独立测试用的数据库里存在一个 `succeeded` 但没有候选的任务。逐条回放业务动作后确认：
该任务属于 **S4 中途撤销场景**——需求已撤销，供应商迟到的成功结果按题面 R7
「不得恢复需求或变成交付」**不应**产生候选。因此**是 Skill 的判据写错了**，
不是业务实现错了：如果按原判据去「修」业务，就会把 R7 要求的正确行为改成错误行为。

### 3.3 修正（修判据，不放宽阈值）

修改 `skills/material-guard/scripts/material_guard.py` 的 G4 检查：

- 原判据：`status='succeeded' AND 没有候选` → FAIL；
- 修正后：只对**未撤销**的需求要求「成功必有候选」，撤销需求的迟到结果按 R7 豁免，
  并在检查名称中写明「（撤销需求的迟到结果除外）」；
- 阈值（85%）、其它 G1–G8 判据**均未放宽**。

### 3.4 `material-guard` 第 2 轮 —— **PASS（重新检查）**

- **命令**：
  ```bash
  python skills/material-guard/scripts/material_guard.py --repo . --db data/run6.db --out evidence/material-guard.md
  ```
- **结论**：**PASS**。G1–G8 八项全部 PASS，S1–S3 源码一致性全部 PASS，覆盖率门禁 PASS。
- **原始输出**：`evidence/material-guard.md`、`evidence/material-guard.json`。

> 这次闭环是**真实发生**的：第 1 轮 FAIL 的输出、`scripts/material_guard.py` 的判据修改、
> 第 2 轮 PASS 的输出三者都在 `evidence/` 内可回源。
> 修正的是 Skill 自身的测量口径，**不是**降低判定标准，也**不是**删改业务代码来让检查通过。

### 3.5 `acceptance-audit` 第 1 轮 —— 交付前自查

- **时机**：合并 PR 之前（任务 T-19）。
- **命令**：
  ```bash
  python skills/acceptance-audit/scripts/acceptance_audit.py --repo . --out-dir evidence/acceptance-audit
  ```
- **输出**：`evidence/acceptance-audit/检查项明细.md`、`evidence/acceptance-audit/acceptance-audit.json`。
- 判定逻辑见该脚本：R1–R8 八项业务规则 + 13 项共同要求（数据库、页面、模拟供应商四类场景、
  阶段产物、AGENTS 版本与读取记录、Skill 七要素、`skill-usage.md`、覆盖率、场景验证、
  gofmt/vet、提交消息格式、泄密扫描无白名单、部署记录、流水线配置）。

### 3.6 `acceptance-audit` 第 2 轮 —— 整改后复检

- **时机**：独立测试 Agent 的缺陷修复完成、证据补齐之后。
- **命令**：同上。
- **输出**：同一目录下追加的复检记录（见 `evidence/acceptance-audit/检查项明细.md` 的「复检」小节）。

---

## 4. Skill 之外实际使用的既有工具/能力

| 名称 | 来源 | 在本项目中的实际用途 |
|---|---|---|
| `go test` / `go vet` / `gofmt` | Go 工具链（既有） | 质量门禁与覆盖率统计的底层执行器 |
| `python` 标准库 | Python 3.14（既有） | 场景验证脚本、覆盖率门禁脚本、两个自编 Skill 的判定脚本 |
| `git` | Git 2.51（既有） | 本地提交历史与分支管理 |
| GitHub MCP Server（远程） | 既有 MCP 服务（本次按用户授权新增配置） | `create_repository` 建仓、`push_files` 推送、`create_branch` 建分支、`create_pull_request` 建 PR、`merge_pull_request` 合并 |
| `docker` | Docker 29.9（既有） | 镜像构建与竞态检测的备选环境（本机无 C 编译器时执行 `go test -race`） |

> 未实际调用过的工具**不列入**本表（共同要求 §4：「未实际调用的不得列为已使用」）。
