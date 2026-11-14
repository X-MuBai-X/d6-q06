# 独立测试缺陷修复 · 第二组（文档与自编 Skill）

> 对应独立测试报告 `evidence/independent-test-report.md` 的 **D-06**、**D-07**。
> 修复方式遵循 AGENTS.md §9：先判定责任方，再选择「修业务」或「修判据」，并保留
> 「发现问题 → 修正 → 复检（负向验证）」的完整闭环。
> 负向验证原始输出见 `evidence/acceptance-audit/负向验证-泄密扫描.txt` 与
> `evidence/material-guard-负向验证.txt`。

---

## D-06（中）文档泄漏明文演示口令 + 泄密扫描规则漏检导致假 PASS

### 现象

1. `docs/01-需求分析.md` 的假设清单 A4 行以明文写出预置账号口令，而
   `AGENTS.md` §5 第 4 条 / §8 第 3 条要求「演示口令的完整值只保留在 `cmd/server/main.go`，
   文档与证据一律以掩码展示」；同仓库 `docs/04-部署与使用说明.md` 已按掩码书写，两处自相矛盾。
2. 更严重：`skills/acceptance-audit/scripts/acceptance_audit.py` 的泄密扫描正则要求
   `口令[:=]` 形态，对「口令统一为 `xxx`」这种写法**不命中**，于是 C-11 判 **PASS**，
   并在 `evidence/acceptance-audit/检查项明细.md` 写下「全量扫描 evidence/ 与 docs/ 未命中
   （未使用白名单或例外目录）」——该结论与事实不符，属于**假通过**。

### 责任方判定

- 事实一属于**交付缺陷**：文档确实泄漏了应掩码的值，直接整改。
- 事实二属于**判据缺陷**：规则依赖「书写格式」而不是「凭据值本身」，任何换一种写法就能绕过。
  规则含义（证据与文档中不得出现明文凭据）没有错，错的是**检测口径**。
  因此两者都改：文档改掩码，判据改为「已知凭据值检测 + 敏感形态检测」双保险，
  **不放宽规则含义，不引入白名单或例外目录**。

### 改动

| 位置 | 改动 |
|---|---|
| `docs/01-需求分析.md` A4 行 | 改为掩码 `Stu****2026`，并注明完整值仅在 `cmd/server/main.go` 的 `demoPassword` 常量 |
| `skills/acceptance-audit/scripts/acceptance_audit.py` | 新增 `known_credentials(repo)`：运行时从 `cmd/server/main.go` 解析演示凭据**真实值**，与形态正则（ghp_/ghu_/github_pat_/Bearer/Authorization/私钥/口令赋值/密钥赋值/Cookie 值等 8 类）双保险比对；扫描范围仍为 `docs/` 与 `evidence/` 全量，**无路径白名单** |
| `skills/acceptance-audit/scripts/acceptance_audit.py` | 新增 C-14「演示凭据明文只存在于 cmd/server/main.go」，对仓库其余位置做同口径比对（同样不做例外目录） |

> 已知凭据值**不写进 Skill 文件**：脚本运行时从 `cmd/server/main.go` 解析，避免检测器自身成为新的泄露点。

### 负向验证（原始输出见 `evidence/acceptance-audit/负向验证-泄密扫描.txt`）

1. 在**仓库副本**（会话工作区临时目录，仓库本身零改动）注入两处人为泄露：
   `docs/_neg-leak-password.md`（明文演示口令）与 `evidence/_neg-leak-token.txt`（`ghp_` 假令牌）。
2. 第 1 次运行：**C-11 FAIL 并点名两个文件**；同时打印对照——旧形态正则命中 `False`、
   新已知值检测命中 `True`，直接证明旧规则确实漏检。
3. 移除人为泄露后第 2 次运行：C-11 恢复 **PASS**。

### 复检

- 仓库实跑 `acceptance-audit`：C-11 **PASS**（已知凭据值比对 1 项 + 8 类形态正则，无白名单）。
- 另在并行任务范围内发现的残留项（`internal/service/service_test.go`、`internal/service/regression_test.go`
  仍含演示口令明文）已一并整改：改为测试专用口令，并新增
  `internal/service/password_test.go`（`TestTestPasswordDiffersFromDemoPassword`、
  `TestNoPlaintextDemoPasswordInPackageFiles`）与 `internal/httpapi/password_test.go` 同类断言，
  确保测试代码与脚本不再复制演示口令明文。

---

## D-07（低）自编 Skill `material-guard` 对 R7 无效

### 现象

对含有「撤销需求被复活为 `generating`/`failed`」缺陷数据的数据库运行
`skills/material-guard/scripts/material_guard.py`，判定 **PASS**（退出码 0），G1–G8 全绿。

### 责任方判定

**判据缺陷**：G1–G8 只检查「撤销需求的候选/采用记录」，不检查撤销需求**是否被改写状态或被再次发起**；
S3 只核对 `internal/domain/rules.go` 里 `CanStartGeneration` 的**声明**，不检查业务层是否**调用**它——
而业务层未调用正是 D-01 的盲区。规则含义（撤销后不得复活）没错，检测口径缺了两个维度。

### 改动

| 位置 | 改动 |
|---|---|
| `skills/material-guard/scripts/material_guard.py` | 新增 **G9**：状态为 `withdrawn` 的需求，其状态不得变为 `generating`/`failed`/`exhausted`，且撤销之后不得再出现 `generation_started`/`generation_timeout` 等推进事件 |
| 同上 | S3 增查 **调用点**：`S3-CallCanStartGeneration`（业务层存在 `CanStartGeneration(` 调用）与 `S3-CallInStartGeneration`（`StartGeneration` 内确实调用了它），不再只看声明 |
| 同上 | 覆盖率证据「待补证」时，整体判定**不得**为 PASS（改为「待补证」，退出码 2） |

### 负向验证（原始输出见 `evidence/material-guard-负向验证.txt`）

1. 在会话工作区临时目录造两个库：`neg-withdrawn-revived.db`（撤销后被复活）与
   `neg-withdrawn-normal.db`（撤销后保持 withdrawn），**未写入仓库 `data/`**。
2. 第 1 次运行（缺陷库）：退出码 1、判定 **FAIL**，G9 明确点名
   `req-neg-1→generating` 与 `req-neg-1:generation_started`；同时 S3 两个调用点检查为 PASS。
3. 第 2 次运行（正常库、覆盖率待补证）：退出码 2、判定 **待补证**（不再是 PASS）。

### 复检

- 对**修复后由当前代码新生成**的库 `data/run8.db` 运行 `material-guard`：
  **判定 PASS，无失败项**（旧库 `data/run6.db` 仍保留着修复前的缺陷数据，G9 会正确 FAIL，
  这正是负向验证的现场证据）。
- `acceptance-audit` 复跑后 C-14 由 FAIL 转为 PASS。

---

## 结论

D-06、D-07 均为**判据/交付侧缺陷**，已修复并完成「发现问题 → 修正 → 复检（负向验证）」闭环；
本轮修复未删除、未跳过任何既有测试，未使用白名单或例外目录规避扫描，
覆盖率门禁在修复后仍为 PASS（整体 86.05%，各核心包均 ≥ 85%）。
