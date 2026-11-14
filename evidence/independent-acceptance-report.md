> **说明**：本报告由独立验收 Agent 在全新上下文生成；其中被引用的演示口令已按 `AGENTS.md` §5 第 4 条替换为掩码（`Stu****2026`），**未改动任何结论、证据位置或数值**。

# 独立验收报告 · 题目 06（营销素材的 AI 生图、重做与确认交付）

> 验收方：独立验收 Agent（全新上下文，未采信开发会话自述结论）
> 验收对象：`C:\Users\26765\Documents\trae_projects\skills\2组\素材\d6`
> 验收日期：2026-11-14（GMT+8）
> 硬性约束遵守情况：**仓库只读**（未修改任何仓库文件、未执行 git 写操作、未提交）；探针脚本、数据库、
> 二进制副本一律建于 `%TEMP%\verify_d6`；结束前已 kill 本次启动的全部 server/mockgen 进程。

---

## 1. 验收环境与被检版本

| 项目 | 值 |
|---|---|
| Git HEAD（验收开始） | `2bc7d52`（`[opt] [v0.1.0] 加固泄密扫描与material-guard的R7不变量`） |
| Git HEAD（验收结束） | `ee067d3`（`[opt] [v0.1.0] 部署脚本探测Python解释器并补阶段检查点与PR描述`） |
| 并发提交影响 | 验收期间**另一开发会话并发提交**。`git diff --name-only 2bc7d52 ee067d3` 仅 3 个文件：`docs/13-阶段检查点与版本索引.md`、`evidence/github-mcp/pr-body.md`、`scripts/deploy.sh`；**`internal/`、`cmd/`、`skills/`、`scripts/verify_scenarios.py` 业务代码零变更**（`git diff --stat 2bc7d52 ee067d3 -- '*.go'` 输出为空）→ 本报告全部结论对两个 HEAD 同时成立 |
| Go | `go1.27.1 windows/amd64` |
| Python | 3.14.8（探针仅用标准库） |
| 数据库 | SQLite（`modernc.org/sqlite`，纯 Go 驱动）；**验收用库全部位于 `%TEMP%\verify_d6\*.db`**，未触碰仓库 `data/` |
| 我的运行实例 | 平台 :18080（clock-scale 0.1）、:18081（clock-scale 1，默认口径）；模拟供应商 :18090、:18091。二进制由 `go build` 输出到 `%TEMP%\verify_d6\bin` |
| 编译器 | `gcc/cc/clang` 均不存在 → `go test ./... -race` 无法执行（环境限制，见 §6） |
| 仓库洁净度 | 验收结束 `git status --short` 显示 ` M evidence/github-mcp/push-exact-dev06.json`、`?? evidence/github-mcp/ci-run-...log`、`?? evidence/github-mcp/pull-request.json` —— **均为并发开发会话产生，非本次验收写入**；本次验收未新增/修改除本报告外的任何仓库文件 |

**本报告的证据文件位置**：全部临时产物在 `%TEMP%\verify_d6\`（`probe*.py`、`probe*.out.log`、`secretscan.out.log`、`leaktest*.out.log`、`mgtest.out.log`、`scenarios-*.log`、`bin/`、`*.db`）。

---

## 2. 六维度逐条结论表

| 维度 | 检查项 | 期望 | 实际 | 结论 | 证据位置 |
|---|---|---|---|---|---|
| 1 业务正确性 | R1–R8 逐条实现 | 8/8 | 8/8 | **通过** | `internal/service/requirement.go:80-167`（R1/R2/R3）、`internal/service/callback.go:39-171`（R4）、`internal/service/requirement.go:223-347`（R5/R6）、`internal/service/requirement.go:376-395`+`callback.go:95-134,210-217`（R7）、`internal/service/service.go:78-91`+`query.go:21-32,64-81`（R8）；实测见 §3、§4 |
| 1 业务正确性 | S1–S5 五组场景可跑通 | 36 条断言全 PASS | 0.1 倍率 36/36；默认倍率 1 36/36（68.6s） | **通过** | `%TEMP%\verify_d6\scenarios-mine.log`、`scenarios-clock1.log`；仓库归档 `evidence/scenario-verify.log`、`evidence/scenario-verify-default-clock.log` 结论一致 |
| 1 业务正确性 | D-01 撤销后不得复活 | 发起被拒 4xx，需求保持 withdrawn | 409，状态仍 withdrawn | **通过（已修复）** | `%TEMP%\verify_d6\probe.out.log`；代码 `internal/service/requirement.go:103-105` |
| 1 业务正确性 | D-02 超时巡检不得改回 failed/exhausted | 保持 withdrawn + 留审计 | 保持 withdrawn；`late_result_ignored` 留痕 | **通过（已修复）** | `%TEMP%\verify_d6\probe.out.log`；代码 `internal/service/callback.go:210-217` |
| 1 业务正确性 | D-04 request_no 不得跨需求串号 | 明确拒绝，不返回他人任务 | 409「该请求编号已属于需求 req-…，不能跨需求复用」；需求 B 任务数 0、状态仍 draft | **通过（已修复）** | `%TEMP%\verify_d6\probe.out.log`；代码 `internal/service/requirement.go:107-122` |
| 2 权限与归属 | 需求人仅访问自己的需求 | 他人需求 4xx | 403 | **通过** | probe.out.log「other requester read -> 403 status=403」 |
| 2 权限与归属 | 未登录 / 伪造令牌 | 401 | 401 / 401 | **通过** | probe.out.log（anonymous、forged token） |
| 2 权限与归属 | 运营无权代替需求人确认采用 | 403 | 403 | **通过** | probe.out.log「operator adopt -> 403」；`internal/service/requirement.go:310-312` |
| 2 权限与归属 | 复核人无权发起生成/需求人无权复核 | 403 | 403 / 403 | **通过** | probe.out.log；`requirement.go:81-83,224-226` |
| 2 权限与归属 | 运营/复核人/需求人的可见范围 | 列表按角色过滤 | 需求人列表不含他人需求；运营可查全部与异常 | **通过** | probe.out.log；`internal/service/query.go:21-32,64-67` |
| 3 数据一致性 | 事务边界 | 写操作原子化、并发写串行 | `BEGIN IMMEDIATE` + WAL + busy_timeout(10s) | **通过** | `internal/store/store.go:32,60-78` |
| 3 数据一致性 | 幂等 | 同需求同 request_no 只建一个任务 | 8 并发同键 → 全部 202、同一 task id、库内仅 1 条任务；供应商「已受理」仅 1 次 | **通过** | `%TEMP%\verify_d6\probe2.out.log`；`%TEMP%\verify_d6\mockgen.err.log` |
| 3 数据一致性 | 唯一处理中任务 | 不同 request_no 并发只允许 1 个 | 8 并发 → 1×202 + 7×409，库内 1 条任务 | **通过** | probe2.out.log |
| 3 数据一致性 | 候选唯一性（is_current） | 同一需求至多 1 个当前候选 | 2 候选时 1 current + 1 history | **通过** | probe.out.log「R6 old candidate becomes history, exactly one current」 |
| 3 数据一致性 | 撤销与迟到回调留痕 | 审计可查 | 事件序列含 `requirement_withdrawn` + `late_result_ignored`；运营异常接口可见 | **通过** | probe.out.log / probe2.out.log；`internal/service/anomaly.go:14-23` |
| 3 数据一致性 | 重启后数据仍可查 | 数据在库 | 重启进程后需求/任务/候选/事件数完全一致 | **通过** | `%TEMP%\verify_d6\probe4.out.log`（delivered 需求 1 task/1 cand/7 events） |
| 4 错误处理 | 非法输入 | 400 | 空活动名 400、描述过短 400、非法 JSON 400、复核无原因 400、空重做原因 400 | **通过** | probe.out.log |
| 4 错误处理 | 非法状态迁移 | 409 | 已交付再确认 409、重复复核 409、已交付撤销 409、exhausted 再发起 409、历史候选复核/确认 409 | **通过** | probe.out.log |
| 4 错误处理 | 重复操作 | 明确结果 | 重复发起幂等返回同一任务；重复回调不重复建候选 | **通过** | probe.out.log / probe2.out.log |
| 4 错误处理 | 鉴权失败 | 401/403，不 500 | 全部 4xx，无 500 | **通过** | probe.out.log |
| 4 错误处理 | 撤销接口连接稳定性（D-03） | 带体 60 次 0 异常 | 带体 0/60、无体 0/60 异常 | **通过（已修复）** | `%TEMP%\verify_d6\probe5.out.log`；`internal/httpapi/api.go:194-201`、`web.go:265-275` |
| 5 可维护性 | gofmt 零差异 | 空输出 | 空输出（exit 0） | **通过** | 我本机执行 `gofmt -l .` |
| 5 可维护性 | go vet 无告警 | 空输出 | 空输出（exit 0） | **通过** | 我本机执行 `go vet ./...` |
| 5 可维护性 | 单测通过 | 全绿 | 7 个包全绿 | **通过** | 我本机 `go test ./... -count=1` |
| 5 可维护性 | 覆盖率整体 ≥85% | ≥85% | **86.1%**（`go tool cover -func` total），与仓库 `evidence/coverage-summary.txt` 86.05% 一致 | **通过** | `%TEMP%\verify_d6\cover.out` |
| 5 可维护性 | 各核心包 ≥85% | 5 个核心包 | domain 89.7 / store 86.8 / service 86.8 / httpapi 89.5 / supplier 87.1 | **通过** | 同上；非核心包 cmd/server 56.3%、cmd/mockgen 80.6%（题面未要求，但见缺陷 L-4） |
| 5 可维护性 | 测试有真实断言与负向边界 | 无空断言 | 118 个 `func Test`；未见 `t.Skip`；断言含状态码、状态机、事件序列、并发计数 | **通过** | `grep t.Skip` 无命中；`internal/service/regression_test.go` 等 |
| 5 可维护性 | 无删测试/弱化断言/覆盖率排除 | 无排除项 | `scripts/coverage_gate.py` 无排除项（仅 `--skip-tests` 复用已有剖面） | **通过** | `scripts/coverage_gate.py:74,86` |
| 5 可维护性 | 竞态检测 | `go test -race` | **未能执行**：`-race requires cgo`，且无 C 编译器 | **未覆盖（环境限制）** | 见 §6 |
| 6 敏感信息 | 仓库内无明文口令/密钥/令牌 | 无 | 源码/文档中仅演示口令 `Stu****2026`（按 AGENTS §5 唯一允许出现在 `cmd/server/main.go`）与默认演示签名密钥；`bin/`、`data/` 均在 `.gitignore` 内未被跟踪 | **有条件通过** | `cmd/server/main.go:24`、`cmd/mockgen/main.go:78`、`docs/04-部署与使用说明.md:61`；`git ls-files` 中无 bin/data |
| 6 敏感信息 | 自编 Skill 泄密扫描有效性 | 能发现泄露 | **部分有效**：docs/evidence 中的 `ghp_…`、口令赋值形态可发现；**新形态密钥落在源码目录不会被发现**（我植入 `sk-live-…` 于 `internal/supplier/client.go` 未被 C-11/C-14 捕获） | **有条件通过（有漏检面）** | `%TEMP%\verify_d6\leaktest.out.log`、`leaktest2.out.log`；`skills/acceptance-audit/scripts/acceptance_audit.py:25-33,169-186,451-486` |

---

## 3. 业务规则 R1–R8 与场景 S1–S5 逐条核对表

| 编号 | 题面要点 | 实现位置 | 我的实测结论 | 判定 |
|---|---|---|---|---|
| R1 | 需求含活动/用途/描述；确认后发起生成；每次生成保存当时描述，历史不被覆盖 | `internal/service/requirement.go:21-51,54-76,132-141`（`DescriptionSnapshot: req.Description`） | 两次生成后任务快照分别为「first version description text」「second version description text」，互不覆盖 | 通过 |
| R2 | 同一需求至多一个处理中任务；同 request_no 重复发起不建第二个供应商任务 | `internal/store/schema.sql:50-58`（`idx_one_processing_task` 部分唯一索引 + `(requirement_id, request_no)` 复合唯一索引）；`requirement.go:107-128` | 8 并发同键 → 1 任务/1 次供应商受理；8 并发异键 → 1×202+7×409 | 通过 |
| R3 | 拒绝/失败/60s 无终态不产生候选；失败或超时后可改描述再发起，最多三次，不自动重试 | `internal/domain/rules.go:9-13,56-61`；`internal/service/callback.go:86-116,176-237`；`requirement.go:129-131` | 拒绝→409/failed/0 候选；失败→failed/0 候选；超时→任务 timeout/需求 failed/0 候选；3 次后 exhausted，第 4 次 409；无自动重试 | 通过 |
| R4 | 成功回调需鉴权、关联已知任务、提供图片地址；重复回调不重复建候选；终态任务不被迟到/冲突回调改写；异常可查 | `internal/httpapi/server.go:222-259`（HMAC 验签）；`internal/service/callback.go:39-171`；`internal/service/query.go:64-81` | 重复回调只产生 1 个候选并留 `callback_duplicate`；未知任务→`callback_unknown_task`；运营异常接口可查 | 通过 |
| R5 | 最新成功结果先由设计复核人审核；未通过须写原因；复核通过后需求人可确认采用或提重做；确认采用才算交付 | `internal/service/requirement.go:223-347`；`internal/domain/rules.go:111-133` | 复核未通过无原因 400；复核通过→approved→adopt→delivered；重复复核 409；运营/复核人 adopt 403 | 通过 |
| R6 | 发起下一次生成后上一版候选只作历史，不可再审核或确认；页面明确当前/历史候选 | `internal/service/callback.go:136-137`（ClearCurrentCandidates）；`requirement.go:251-254,324-326`；`internal/httpapi/templates/requirement.html:26-65` | 历史候选复核/确认均 409「该候选已不是当前候选」；页面模板区分「当前候选」与「历史候选（只作历史，不能再审核或确认）」 | 通过 |
| R7 | 确认采用前可撤销；撤销后供应商迟到结果保留审计但不得恢复需求/形成交付；已采用不可撤销 | `internal/service/requirement.go:376-395`；`internal/service/callback.go:93-134,208-217`；`internal/domain/rules.go:44-52` | 撤销后发起 409、状态仍 withdrawn；超时巡检后仍 withdrawn（仅任务 timeout + `late_result_ignored`）；迟到成功 0 候选；已交付撤销 409 | 通过 |
| R8 | 需求人仅访问自己的需求；复核人可审核待处理需求；运营查看生成及异常记录；可按需求查清每次描述、任务、结果、复核与最终处置 | `internal/service/service.go:78-103`；`internal/service/query.go:21-60` | 需求人列表/详情均不含他人需求（403）；复核队列只列 candidate_pending；运营总览与异常可查；需求详情含 tasks/candidates/events 全量 | 通过 |
| S1 | 正常交付：创建→生成→设计通过→确认→运营查询 | — | 7/7 断言 PASS | 通过 |
| S2 | 业务重做：退回或需求人不采用→改描述再生成→新版本确认，旧版本不可误交付 | — | 6/6 PASS（含历史候选复核/确认 409） | 通过 |
| S3 | 技术失败：拒绝/失败/超时→重试→历史原因可追溯 | — | 9/9 PASS（含三次上限与第四次 409） | 通过 |
| S4 | 中途撤销：生成中撤销→迟到成功→需求保持撤销，运营可查处理原因 | — | 5/5 PASS | 通过 |
| S5 | 并发与权限：重复发起、旧回调晚到、重复复核、他人需求访问、第三次后再次重做 | — | 9/9 PASS | 通过 |

---

## 4. 独立复测 D-01 / D-02 / D-04：复现脚本与真实输出

### 4.1 方法

- 探针脚本：`%TEMP%\verify_d6\probe.py`（60 项断言）、`probe2.py`（12 项：重复回调 / 供应商拒绝 / 并发）、`probe5.py`（D-03 连接稳定性 120 次）、`probe6.py`（页面表单通道）；全部用标准库 `urllib` 直接打真实 HTTP 接口。
- 运行方式：`go build` 出的 `server.exe` + `mockgen.exe`（真实模拟供应商，含 `mode=success/failure/reject/timeout`、`duplicate`、`redeliver` 控制面），平台 `-clock-scale 0.1`（6s 超时）用于快速触发巡检；默认 `-clock-scale 1`（60s 题面口径）另起实例复核。
- 数据库：全新 `%TEMP%\verify_d6\probe.db`，未使用仓库 `data/`。

### 4.2 D-01（撤销后不得再发起生成）— 已修复

```
[PASS] D-01 start -> 202
[PASS] D-01 withdraw -> 200
[PASS] D-01 status == withdrawn :: withdrawn
[PASS] D-01 restart after withdraw rejected 4xx (not 202) :: status=409
      body={"error":"操作冲突: 当前状态 withdrawn 不允许发起生成","ok":false}
[PASS] D-01 requirement NOT revived :: withdrawn
[PASS] D-01 update description after withdraw rejected 4xx :: status=409
```
对应代码：`internal/service/requirement.go:103-105`（`if req.Status != domain.ReqGenerating && !domain.CanStartGeneration(req.Status)` → `ErrInvalidState`→409）。
补充：模拟供应商日志 `%TEMP%\verify_d6\mockgen.err.log` 中，撤销后的那次请求**没有**产生新的「已受理」记录，即未向供应商提交。

### 4.3 D-02（超时巡检不得把已撤销需求改回 failed/exhausted）— 已修复

```
[PASS] D-02 status still withdrawn after sweep :: status=withdrawn seen=['withdrawn']
[PASS] D-02 late_result_ignored audit present ::
      ['requirement_created','generation_started','callback_accepted','requirement_withdrawn','late_result_ignored']
[PASS] D-02 task marked timeout, requirement not failed/exhausted :: tasks=[["task-943f...","timeout"]]
[PASS] mock redeliver late callback -> 200
[PASS] D-02 late success callback keeps withdrawn :: withdrawn
[PASS] D-02 late success creates no candidate :: []
[PASS] D-02 late success audited as late_result_ignored ::
      [... 'late_result_ignored','callback_conflict']
```
观察到的状态集合只有 `['withdrawn']`——巡检全程未出现 failed/exhausted。
对应代码：`internal/service/callback.go:210-217`（撤销需求只记 `late_result_ignored`，不改写需求状态）。
补充观察：任务已被巡检判为 `timeout`（终态）后，迟到的**成功**回调走的是「终态任务不被改写」分支，记为 `callback_conflict`（而非 `late_result_ignored`）；两种异常都进了运营异常列表，语义正确但事件种类不同，属可接受的实现差异。

### 4.4 D-04（幂等键 request_no 不得跨需求串号）— 已修复

```
[PASS] D-04 reqA first start -> 202
[PASS] D-04 reqB reusing same request_no rejected 4xx :: status=409
      body={"error":"操作冲突: 该请求编号已属于需求 req-3be43b0c4311ec4591e00183，不能跨需求复用","ok":false}
[PASS] D-04 rejected response does not leak reqA task object
[PASS] D-04 reqB has zero tasks :: []
[PASS] D-04 reqB status not generating :: draft
[PASS] D-04 same-requirement replay idempotent (same task) :: status=202 task=task-92fb9df4e9384ea26bc907ef
```
对应代码：`internal/service/requirement.go:107-122`（需求内命中 → 幂等返回；跨需求命中 → `ErrConflict` 409）；DB 侧 `internal/store/schema.sql:56-58` 已把全局唯一索引换成 `(requirement_id, request_no)` 复合唯一索引。
供应商侧佐证：`%TEMP%\verify_d6\mockgen.err.log` 中同一 `ref=task-92fb9df4…` 只出现 **1 次**「已受理」。

### 4.5 探针自纠记录（透明说明）

`probe2.py` 首轮出现 1 项 FAIL：「retry after reject allowed -> 202 :: status=409」。经复核，这是**探针自身的错误**（把模拟供应商留在 `mode=reject` 就再次发起，第二次自然仍被供应商拒绝），不是产品缺陷。把供应商切回 `mode=success` 后重测（`probe3.out.log`）：

```
retry after reject (supplier mode=success): 202 attempt_no=3
status: candidate_pending attempts: 3 cands: 1
tasks: [(1,'rejected','REJ-1'), (2,'rejected','REJ-2'), (3,'succeeded','REJ-RETRY-3')]
```
即「供应商拒绝→需求 failed→可修改描述再发起→成功」链路正确。

### 4.6 D-05 / D-03 / D-07 / D-08 修复的独立复测

| 缺陷 | 复测方式 | 结果 |
|---|---|---|
| D-03 撤销接口丢连接 | 60 次带请求体 + 60 次不带请求体 | 异常 0/60 与 0/60（`probe5.out.log`） |
| D-05 默认时钟下场景脚本不通过 | 新实例 `-clock-scale 1` + 全新库，跑仓库 `scripts/verify_scenarios.py` | 36/36 PASS，耗时 68.6s，退出码 0 |
| D-07 material-guard 对 R7 无效 | 手工构造含 D-01/D-02 违规的库（撤销后 `generation_started` 且状态被改写为 `generating`）跑 `material_guard.py` | 判定 **FAIL**，G9 明确报出「撤销后状态被改写的需求：['req-bad→generating']；撤销后仍出现推进事件：['req-bad:generation_started']」，exit 1；对我的干净库判 PASS（exit 2，待补证，因 `--skip-tests` 未跑覆盖率） |
| D-08 mockgen 单测固定端口 | 读码 + 全量 `go test ./...` | `cmd/mockgen/main_test.go:184` 已改为 `127.0.0.1:0` 动态端口；全量单测全绿 |
﻿﻿
---

## 5. 缺陷清单

> 说明：以下均为**我独立复现或独立读码确认**的问题；不包含开发方自述已修复但我未能证实的项（前者已实测证实，见 §4）。**本次未修改任何缺陷，仅报告。**

| 编号 | 严重度 | 缺陷 | 触发条件 | 影响 | 证据位置 | 建议 |
|---|---|---|---|---|---|---|
| L-1 | 低 | 自编 Skill 的泄密扫描存在**漏检面**：`SECRET_PATTERNS`（形态正则）只作用于 `evidence/` 与 `docs/`（`scan_secrets(repo, ["evidence","docs"])`），C-14 全仓扫描只比对**已知演示凭据值**。把一个**新的**密钥形态写进源码目录不会被任何一项捕获 | 在 `internal/`、`cmd/`、`skills/` 内写入 `const hardcodedAPIKey = "sk-live-…"` 形态的密钥 | 泄密自检对「源码中新引入的密钥」形同虚设；与共同要求 §3「禁止泄露密钥」的自检手段不匹配 | `%TEMP%\verify_d6\leaktest2.out.log`（植入后 C-11/C-14 仍 PASS，只有 `internal/service/auth.go` 里的已知演示口令被 C-14 抓到）；`skills/acceptance-audit/scripts/acceptance_audit.py:25-33,169-186,451-486` | 把形态正则也应用到**全仓库**（沿用现有「按内容判二进制、不按路径做白名单」的做法），并保留 C-11/C-14 的分工 |
| L-2 | 低 | `acceptance-audit` 的 R2-01 判定为**字符串存在性**匹配：`has_unique_index = "idx_one_processing_task" in schema and "idx_task_request_no" in schema`。当前 schema 里 `idx_task_request_no` 只以 `DROP INDEX IF EXISTS` 一行残留，且 R2 的**跨需求串号**（D-04）根本没有对应判定项 | 现状即如此（`internal/store/schema.sql:56`） | 该 Skill 对 R2 给 PASS 时，**不能证明** D-04 已修；属于「判据与代码组织方式不一致」型假阳性风险（与上一轮 D-06/R4-01 同类问题） | `skills/acceptance-audit/scripts/acceptance_audit.py:210-217`；`internal/store/schema.sql:50-58` | 增加「跨需求 request_no 复用必须被拒」的源码/行为判定；把唯一索引判定改为按**当前生效的索引定义**匹配 |
| L-3 | 低 | `acceptance-audit` 的 C-08（覆盖率）与 C-09（五组场景）**只读取既有证据文件的结论**，不自行复算/复跑 | 证据文件结论与实际不符时仍会判 PASS | 自证循环：审计结论不可作为独立证据（我已独立复算覆盖率并重跑场景脚本，结论一致，故本仓库当前**无实际危害**） | `skills/acceptance-audit/scripts/acceptance_audit.py`（C-08/C-09 判定段）；对照 `evidence/coverage-summary.txt`、`evidence/scenario-verify.log` | C-08 改为直接解析 `evidence/coverage.out` 复算阈值；C-09 至少校验场景日志的断言数与失败数 |
| L-4 | 低 | 非核心包覆盖率偏低：`cmd/server` 56.3%（40/71）、`cmd/mockgen` 80.6%（125/155）。题面只对「整体 + 各核心业务包」设 85% 门槛，故**不构成红线**，但 `cmd/server` 是进程入口（含 flag 解析、seed、优雅关闭），主干路径的单测偏薄 | — | 入口层的参数校验/错误分支缺回归保护 | 我本机覆盖率输出 `%TEMP%\verify_d6\cover.out`；`evidence/coverage-summary.txt` 同样记录 56.34%/80.65% | 补 `cmd/server` 的 flag 解析、seed 幂等、优雅关闭路径单测；或明确声明其非核心并写入门禁说明 |
| L-5 | 低 | 文档与证据**内容陈旧**（结论本身不错）：`docs/10-交付与发布说明.md` §6.2 称 `evidence/scenario-verify.log` **不存在**、§6.4 称 `evidence/independent-test-report.md` **不存在**（两者现均存在）、§6.1 覆盖率写 85.63%（现行证据为 86.05%）；`skill-usage.md`「使用过程」栏仍写 material-guard 只有 **G1–G8**（现已新增 G9） | 读交付文档的人按现状判断 | 文档结论与仓库现状不一致，交付索引的可信度下降 | `docs/10-交付与发布说明.md:184,204,220-222`；`skill-usage.md:11`；`skills/material-guard/SKILL.md:49-52`（G9 已存在） | 交付前用一次脚本生成/刷新 docs/10 与 skill-usage 的数字与文件状态 |
| L-6 | 低（信息） | 演示口令与默认供应商签名密钥以明文常量和 flag 默认值存在于源码/文档：`cmd/server/main.go:24`（`demoPassword`）、`cmd/server/main.go:43`、`cmd/mockgen/main.go:78`、`docs/04-部署与使用说明.md:61` | 任何人克隆仓库即可拿到演示凭据 | **教学设定**，且 `AGENTS.md` §5 明确要求完整值只保留在 `cmd/server/main.go`；`bin/`、`data/` 已被 `.gitignore` 排除（`git ls-files` 确认未跟踪），故不构成「密钥泄露」违规；但真实部署必须改为环境注入 | `cmd/server/main.go:24,43`；`cmd/mockgen/main.go:78`；`.gitignore` | 保持现状可接受；在 `docs/04` 补一句「生产部署必须以环境变量覆盖 `-seed-password` / `-supplier-secret`」 |

**缺陷统计：0 严重 / 0 中等 / 6 低（含 1 条信息项）。**

---

## 6. 未覆盖范围与原因

| 未覆盖项 | 原因（可回源） | 影响 |
|---|---|---|
| `go test ./... -race` 数据竞争检测 | 本机 `go test ./... -race` → `go: -race requires cgo; enable cgo by setting CGO_ENABLED=1`；`where gcc/cc` → 均不存在（`INFO: Could not find files for the given pattern`）；无 Docker 守护进程，无法改用 golang 容器 | 「并发唯一性」只有**功能级**验证（我做了 8×2 组并发实测，结果正确），**未做数据竞争检测**。不能写成「竞态检测通过」 |
| GitHub 远端链路（建仓/分支/PR/合并/流水线/分支保护） | 本机无 GitHub 凭据与可用连接；仓库 `evidence/github-mcp/` 归档报文我只能**读到**、无法到远端复核 | 共同要求 §8 的远端项、§7 的「仅对合并后的主分支版本发布」**未证实** |
| 自动部署与线上冒烟 | `evidence/deployment-main.log` **不存在**；`scripts/deploy.sh` 与 `.github/workflows/ci.yml` 只是**配置存在**、未见真实运行记录 | 共同要求 §7 与「七条红线」第 5 条**未覆盖**；本报告不据此判「已部署成功」 |
| meegle CLI 工作项编号核验 | 提交消息中的编号一律为 `#TBD` 占位（`docs/10-交付与发布说明.md` §2 如实登记 CLI 未安装） | 共同要求 §9 未覆盖 |
| 讲解视频本体 | 仓库只有 `docs/12-讲解视频带时间点目录.md`（目录），无视频文件 | 共同要求 §10 的「视频」项未覆盖 |
| 页面通道的浏览器端到端视觉验收 | 我只做了表单通道的 HTTP 级验证（登录/创建/工作台 200，模板「当前候选/历史候选」分区在 `templates/requirement.html` 中确认），**未在真实浏览器渲染核对** | 页面视觉与交互细节未覆盖 |
| 长稳/压力 | 仅做约 250 次写请求与 16 组并发请求的规模 | 不构成压力测试结论 |

---

## 7. 总体结论

**结论：有条件通过（PASS with conditions）。**

理由：

1. **题面业务红线（R1–R8、S1–S5）全部通过**：R1–R8 我逐条读码定位并实测；仓库自带场景脚本在**两种时钟倍率**下均由我亲自跑出 36/36 PASS（0.1 倍率一次、默认 60s 口径一次 68.6s）。
2. **上一轮独立测试检出的严重/中等问题确已修复且我独立复现验证**：D-01（409 + 需求保持 withdrawn）、D-02（巡检后仍 withdrawn + 留痕）、D-04（409 拒绝 + 不返回他人任务 + 供应商仅受理 1 次）均以真实 HTTP 探针复现通过；附带复测 D-03（120 次 0 异常）、D-05（默认时钟 36/36）、D-07（违规库判 FAIL、干净库判 PASS）、D-08（动态端口、单测全绿）亦成立。
3. **权限、数据一致性、错误处理三维度实测无缺陷**：越权全部 4xx（403/401），非法输入/非法状态迁移/重复操作均有明确 4xx 且无 500；事务用 `BEGIN IMMEDIATE`，并发同键只落 1 条任务、异键 1×202+7×409；候选 `is_current` 唯一；撤销与迟到回调均留审计；重启后数据完整可查。
4. **质量门禁达标**：gofmt 零差异、go vet 无告警、单测全绿、整体覆盖率 86.1%（各核心包 86.8%–89.7%，均 ≥85%），与仓库 `evidence/coverage-summary.txt` 的结论与数字一致（未发现自证偏差）；未见 `t.Skip`、删测试、弱化断言或覆盖率排除项。
5. **保留条件（不判整题无条件通过的原因）**：
   - **竞态检测未执行**（本机无 C 编译器，§6），并发正确性只有功能级证据；
   - **自动部署与远端 Git 链路未证实**（缺 `evidence/deployment-main.log`，无 GitHub 访问），共同要求「七条红线」第 5、6 条**不能由本次验收判定为已满足**；
   - 自编 Skill 存在 3 项**判据强度不足**（L-1 泄密扫描漏检面、L-2 R2 判定假阳性风险、L-3 覆盖率/场景项自证循环），属「检查工具可信度」问题而非业务缺陷；
   - 6 项低严重度文档/覆盖/凭据管理问题待整改（L-4~L-6）。

因此：业务正确性与工程质量判**通过**；流程与部署类红线项判**未覆盖**，需在具备 C 编译器与 GitHub/部署环境的后续环节补证后再作最终判定。

---

## 附录 A：本次验收执行的关键命令与输出摘要

```
> gofmt -l .                       → （空输出）, exit 0
> go vet ./...                     → （空输出）, exit 0
> go test ./... -count=1           → ok ×7（mockgen/server/domain/httpapi/service/store/supplier）
> go test ./... -race -count=1     → go: -race requires cgo; enable cgo by setting CGO_ENABLED=1（环境限制）
> go test ./... -coverprofile=%TEMP%\verify_d6\cover.out -covermode=atomic
      mockgen 80.6 / server 56.3 / domain 89.7 / httpapi 89.5 / service 86.8 / store 86.8 / supplier 87.1
> go tool cover -func=%TEMP%\verify_d6\cover.out | tail -1  → total: (statements) 86.1%
> python scripts/verify_scenarios.py --base http://127.0.0.1:18080 --mock http://127.0.0.1:18090 --password ***
      → 通过 36 项，失败 0 项（clock-scale 0.1）
> python scripts/verify_scenarios.py --base http://127.0.0.1:18081 --mock http://127.0.0.1:18091 --password ***
      → 通过 36 项，失败 0 项（clock-scale 1，耗时 68.6s）
> python %TEMP%\verify_d6\probe.py    → total=60 pass=60 fail=0
> python %TEMP%\verify_d6\probe2.py   → total=12 pass=11 fail=1（该 1 项为探针自身错误，见 §4.5）
> python %TEMP%\verify_d6\probe3.py   → 供应商切 success 后重试 202（复核 §4.5）
> python %TEMP%\verify_d6\probe5.py   → 撤销带体 0/60、无体 0/60 异常
> python skills/material-guard/scripts/material_guard.py --db %TEMP%\verify_d6\bad-revived.db --skip-tests
      → 判定 FAIL（G9 命中撤销复活），exit 1
> python skills/material-guard/scripts/material_guard.py --db %TEMP%\verify_d6\probe.db --skip-tests
      → G1–G9 全 PASS，整体「待补证」（覆盖率未跑），exit 2
> 泄密扫描（植入样本的副本仓库，非真仓库）
      docs/ 植入 password: …、evidence/ 植入 ghp_… → C-11 FAIL，命中 2 处，总判定 FAIL，exit 1
      internal/ 植入 sk-live-… 新形态密钥 → C-11/C-14 均未命中（漏检，见 L-1）
```

## 附录 B：清理声明

- 本次启动的 4 个进程（server :18080、server :18081、mockgen :18090、mockgen :18091）已全部 `Stop-Process` 终止；`Get-Process server,mockgen` 已无残留。
- 全部探针脚本、数据库、二进制、日志位于 `%TEMP%\verify_d6\`，**未写入仓库**；仓库内仅新增本报告 `evidence/independent-acceptance-report.md`。
- 仓库 `git status` 中的 `M evidence/github-mcp/push-exact-dev06.json` 与两个未跟踪文件来自**并发的开发会话**，非本次验收产生，本次未触碰。
