All checks are complete, the repo is clean at HEAD `621c85f`, and my test processes are shut down. Here is my report.

---

# 独立测试报告 · 题目 06

## 0. 测试环境与被测版本

| 项目 | 值 |
|---|---|
| Go 版本 | `go1.27.1 windows/amd64` |
| Python | 3.14.8（仅用标准库写探针脚本） |
| 被测 git HEAD | 起始 `8769b79`；测试期间**另一会话并发提交**至 `621c85fbe644bc1cb6083e2740fbb18d690299e2`（工作区曾出现 5 个文件被并发修改）。我已用 `git diff --name-only 8769b79 621c85f` 复核：**缺陷相关生产文件（requirement.go / callback.go / api.go / web.go / rules.go / tasks.go / tx.go / docs/01 / verify_scenarios.py）在最终 HEAD 均未改动**，并用最终工作区重新编译复测了全部缺陷（V1/V2/V3）。 |
| 被测二进制 | 自 `go build` 构建到 `%TEMP%\d6bin` / `d6bin2`（不写仓库） |
| 运行实例 | 5 个隔离实例，端口 18080/18180/18280/18380/18480/18580 + 对应 mock 端口；数据库全部为 `%TEMP%\ind-*.db` 全新库 |
| 教学时钟 | `-clock-scale 0.1`（6s 超时上限）与默认 `1`（60s）各跑一轮 |

**环境说明（须披露）**：我接手时仓库 `bin/` 下有开发者遗留的 `server.exe`/`mockgen.exe` 常驻 8080/8090（数据库为仓库内 `data/run6.db`，已被污染）。第一次跑 `verify_scenarios.py` 时请求全部打到该遗留进程，导致 2 项假失败。我已终止这些遗留进程并在隔离端口上用全新库重测；**该 DB 中 `s5-same` 已被占用的事实反而复现了 D-04**。仓库源码/测试/文档我未做任何修改（`git status` 全程仅显示并发会话的改动）。

---

## 1. 执行的命令与原始输出摘要

### 1.1 gofmt / vet / test（最终 HEAD 工作区）

```
> gofmt -l .
(空输出)                                   gofmt exit=0

> go vet ./...
(空输出)                                   vet exit=0

> go test ./... -count=1
ok  creative-studio/cmd/mockgen      5.809s
ok  creative-studio/cmd/server       1.666s
ok  creative-studio/internal/domain  1.141s
ok  creative-studio/internal/httpapi  1.217s
ok  creative-studio/internal/service 1.495s
ok  creative-studio/internal/store   3.203s
ok  creative-studio/internal/supplier 4.320s
test exit=0
```

> 注：一次全量重跑出现 `cmd/mockgen TestRunStartsAndStops FAIL: listen tcp 127.0.0.1:18099: bind ...`，单独重跑即 PASS —— 属端口硬编码冲突（D-08）。

### 1.2 覆盖率门禁（`python scripts/coverage_gate.py --root . --out-dir %TEMP%\d6-cov2 --min 85`）

```
creative-studio/cmd/mockgen          80.79%  (122/151)
creative-studio/cmd/server           57.97%  (40/69)
creative-studio/internal/domain      89.71%  (61/68)
creative-studio/internal/httpapi     89.69%  (513/572)
creative-studio/internal/service     85.43%  (422/494)
creative-studio/internal/store       86.46%  (300/347)
creative-studio/internal/supplier    87.10%  (27/31)
【整体覆盖率】 85.74%  (1485/1732)
【结论】PASS（整体与各核心包均 ≥ 85.0%）      gate exit=0
```
统计口径：脚本先删旧剖面、用绝对路径重生成，解析剖面按语句数统计，无排除项。

### 1.3 场景脚本 `scripts/verify_scenarios.py`

- **全新库 + `-clock-scale 0.1`**（脚本要求）：`通过 36 项，失败 0 项`，`exit=0`。
- **全新库 + 平台默认 `-clock-scale 1`**：`通过 35 项，失败 1 项`
  `[FAIL] S3 第三次未成功后达到次数上限 status=generating`，`exit=1`（D-05）。

### 1.4 自编 Skill 实跑（输出写到 `%TEMP%`，未写仓库）

- `skills/material-guard/scripts/material_guard.py --db %TEMP%\ind-C.db`（该库含 D-01/D-02 缺陷数据）→ **判定 PASS，exit 0**（D-07）。
- `skills/acceptance-audit/scripts/acceptance_audit.py --skip-toolchain --out-dir %TEMP%\d6-aa` → 19 PASS / 0 FAIL / 3 待补证，exit=2；其中 **C-11「证据与文档中无明文口令」判 PASS**，但我的独立扫描在 `docs/01-需求分析.md:163` 命中明文口令（D-06）。

---

## 2. 用例清单表

|编号 | 场景/规则 | 操作步骤 | 期望 | 实际 | 结论 |
|---|---|---|---|---|---|
| S1–S5 | 仓库自带五组场景（36 项断言） | 全新库 + `-clock-scale 0.1` | 全部 PASS | 36/36 PASS，exit=0 | ✅ |
| S3′ | 同上，平台**默认** `-clock-scale 1` | 全新库 + 默认参数 | 脚本应通过 | 35/36，`S3 第三次未成功后达到次数上限 status=generating`，exit=1 | ❌ D-05 |
| T1 | R2 同一 request_no 重复发起 | 同一 request_no 发起 3 次，比对 mockgen「已受理」日志 | 供应商只调用 1 次，返回同一 task | 受理计数 delta=1，返回同一 task，平台仅 1 条任务 | ✅ |
| T2 | R3 供应商拒绝 | mock `mode=reject` 后发起 | 409、需求 failed、任务 rejected、有失败原因、无候选、有审计 | 全部符合（409 / failed / rejected / 有原因 / 0 候选 / `generation_rejected`） | ✅ |
| T3 | R3 60s 超时（0.1→6s）且不自动重试 | mock `mode=timeout`，等待巡检 | 需求 failed、任务 timeout、无候选、无自动重试 | 全部符合；等待 6s 后供应商受理计数不变 | ✅ |
| T4 | R3 第 3 次失败后再次发起 | 连续3 次失败后再发起 | 4xx拒绝、任务仍为 3 | 409「已发起 3 次生成，达到上限」，任务数=3 | ✅ |
| T5 | R7 撤销后迟到成功 / 迟到失败 | 撤销后 `/control/redeliver` 分别投递 succeeded / failed | 保持 withdrawn、无候选、留审计 | 两种均保持 withdrawn、0 候选、事件含 `late_result_ignored` | ✅ |
| T6/U2/V1 | **R7 撤销后再次发起生成** | 撤销后用新 request_no 发起 | 4xx | **202，需求被复活为 `generating`，新增 1 条任务** | ❌ **D-01** |
| T7/U3 | 撤销（生成中）后再次发起 | 撤销后新 request_no 发起 | 4xx | 409（仅因存在处理中任务而冲突，非显式状态判定） | ⚠️ 偶然通过 |
| U4/V1 | 失败后撤销再发起 | failed→withdrawn→发起 | 4xx | **202，状态被复活为 `generating`** | ❌ **D-01** |
| U1/V2 | **R7 超时巡检不得复活已撤销需求** | 生成中撤销，等待巡检 | 保持 withdrawn | **t+9s 变为 `failed`，事件出现 `generation_timeout`** | ❌ **D-02** |
| T8 | R8 复核历史候选 / 访问他人需求 / 运营确认采用 / 需求人自行复核 | 显式指定历史 candidate_id 复核；他人 GET；运营 adopt；需求人 review | 全部 4xx | 409 / 401 / 403 / 403 | ✅ |
| T9 | R4 重复回调 | mock `duplicate=true` | 只1 个候选、留审计 | 候选=1，事件含 `callback_duplicate` | ✅ |
| T10 | R4 回调签名错误 | 错误签名回调 | 拒收、可查异常 | 401，运营可见 `signature_invalid` | ✅ |
| T15 | R4 关联不到已知任务 | 正确 HMAC + 未知 task_id | 忽略、不 5xx、可查异常 | 200 `ignored=true`，异常种类 `callback_unknown_task` 可查 | ✅ |
| T12 |持久化 | 重启进程后查同一需求 | 数据仍在 | 4 个需求状态/任务/候选/事件数完全一致 | ✅ |
| T16 | 幂等键作用域 | 两个不同需求用同一 request_no | 不应跨需求串号 | **rid2 拿到 rid1 的 task id，rid2 保持 draft、0 任务** | ❌ **D-04** |
| T13/T14 | R7 已交付不可撤销 | delivered 后撤销 | 4xx | 409 | ✅ |
| V3 | 撤销接口稳定性 | 带请求体撤销 40 次 / 不带体 60 次 | 无连接错误 | **带体 5/40 次 `ConnectionResetError`；不带体 0/60** | ❌ **D-03** |
| 泄密 | docs/ 与 evidence/ 全量扫描（35 文件，无白名单） | 9 类敏感形态正则 | 无明文口令/令牌 | **命中 1 处：`docs/01-需求分析.md:163` 明文 `Stu****2026`** | ❌ **D-06** |
| 反作弊 | 删失败测试/弱化断言/跳过/排除生产代码 | git 历史 + 测试文件扫描 | 无 | 无文件被删改；无 `t.Skip`；覆盖率脚本无排除项 | ✅ |

---

## 3. 发现的缺陷

### D-01（严重）撤销后仍可发起生成，已撤销需求被复活为 `generating`
- **触发条件**：需求人先撤销自己的需求（状态 `withdrawn`），再用**新的 request_no** 发起生成。
- **期望**：按题面 R7 与 `AGENTS.md` §4 状态表（`withdrawn` 行「是否可发起生成＝否」），必须返回 4xx，需求保持 `withdrawn`。
- **实际**：返回 **202**，需求状态被改写为 `generating`，并真的向供应商提交了任务、新建 1 条生成任务。
- **证据**：
  - `internal/service/requirement.go:92-118`：`StartGeneration` 事务内只校验「归属（97）→ 幂等键命中（101）→ 是否已有 processing 任务（111）→ 次数上限（116）」，**从未调用** `internal/domain/rules.go:26-32` 的 `CanStartGeneration`（该函数已正确排除 `withdrawn`）。
  - 复现输出（最终树重新编译复测）：
    ```
    ### V1 撤销后再次发起生成（当前树复现）
       撤销: 200
       撤销后状态: withdrawn
    [FAIL] V1 撤销后发起应 4xx | status=202
    [FAIL] V1 撤销后状态未被复活 | generating
    ```
  - U4（failed→withdrawn→发起）同样 202 / `generating`。
- **建议修法**：在 `StartGeneration` 事务内、`FindProcessingTaskTx` 之前加入  `if !domain.CanStartGeneration(req.Status) { return fmt.Errorf("%w: 当前状态 %s 不允许发起生成", domain.ErrInvalidState, req.Status) }`
  （`ErrInvalidState` 已映射为 409）；并补一条「撤销后发起返回 409」的单元测试。

### D-02（严重）超时巡检把已撤销的需求复活为 `failed` / `exhausted`
- **触发条件**：生成任务处于 `processing` 时撤销需求，随后超时巡检触发（`-clock-scale 0.1` 即6 秒后）。
- **期望**：R7「撤销后……不得恢复需求」，需求应保持 `withdrawn`。
- **实际**：需求状态在 t+9s 由 `withdrawn` 变为 `failed`，并写入 `generation_timeout` 事件。
- **证据**：
  - `internal/service/callback.go:203-214`：`SweepTimeouts` 拿到 `req` 后**未判断 `req.Status == domain.ReqWithdrawn`**，直接 `UpdateRequirementStatusTx(..., NextStatusAfterAttemptFailure(current.AttemptNo), ...)` 把需求改为 `failed`/`exhausted`。
  - 复现输出：
    ```
    ### V2 撤销(生成中)后超时巡检
       生成后: generating / 撤销: 200
       t+9s 状态=failed
    [FAIL] V2 撤销后经巡检仍为 withdrawn | failed
       事件: [... 'requirement_withdrawn', 'generation_timeout']
    ```
- **建议修法**：在 `SweepTimeouts` 的事务内，若 `req.Status == domain.ReqWithdrawn` 则只把任务置为 `timeout` 并记 `late_result_ignored`，不改写需求状态。

### D-03（中）撤销接口不读取请求体，客户端约 5–15% 概率收到 TCP 连接重置
- **触发条件**：调用 `POST /api/requirements/{id}/withdraw` 时携带请求体（如 `{}`）。
- **期望**：稳定返回 200/4xx。
- **实际**：服务端业务已生效（随后查询状态确为 `withdrawn`），但客户端偶发 `ConnectionResetError [WinError 10054]`，页面/脚本侧表现为网络错误，且服务端日志无任何记录。
- **证据**：
  - `internal/httpapi/api.go:182-188`：`apiWithdraw` 直接调用业务层，**从不读取请求体**；`internal/httpapi/web.go:254-258`：`formWithdraw` 也**未调用 `r.ParseForm()`**。Go `net/http` 在请求体未被读完时关闭连接会发 RST，客户端可能丢失已生成的响应（其余 JSON处理器都调用了 `decodeJSON`，因此不复现）。
  - 对照实验（同一实例、同一脚本）：
    ```
    带请求体: 连接重置 9/60
    无请求体: 连接重置 0/60
    V3 带请求体撤销: 5/40 次重置
    ```
  - 连带影响：仓库自带 `scripts/verify_scenarios.py:214` 正是带 `{}` 调撤销，我第一次跑该脚本即在 S4 处抛出 `ConnectionResetError` 并整脚本中断（`scripts/verify_scenarios.py` 第 214 行 traceback）。
  - 排除环境因素的证据：对 mockgen 120 次 POST、对平台只读端点 120 次 POST 均 0 次重置；用裸 socket 客户端发同样的请求序列 60/60 成功（差别在于是否发送请求体）。
- **建议修法**：`apiWithdraw` 中先 `_, _ = io.Copy(io.Discard, r.Body)`（或统一走 `decodeJSON`），`formWithdraw` 中先 `r.ParseForm()`；并对每个写接口补「带体请求不丢响应」的回归用例。

### D-04（中）幂等键 `request_no` 未按需求隔离，跨需求复用会串号并静默不执行
- **触发条件**：用**已存在的 request_no** 对**另一个需求**发起生成。
- **期望**：新需求应正常发起，或明确拒绝；任何情况下不得返回别的需求的任务对象。
- **实际**：返回 **202 且 task 指向另一个需求**（`requirement_id` 为 A 的任务），新需求 B 状态仍是 `draft`、任务数 0、供应商也不会被调用 —— 一次「成功」的202 却什么也没做。
- **证据**：
  - `internal/service/requirement.go:101-106` 用 `GetTaskByRequestNoTx` 全局按 `request_no` 命中即返回，未核对 `task.RequirementID == requirementID`；`internal/store/tasks.go:34-38`、`internal/store/tx.go:61-73` 的 SQL 只按 `request_no` 过滤。
  - 复现输出：
    ```
    T16 rid1: req-954acd10… status 202 task task-f4b209aa… req req-954acd10…
    T16 rid2: req-6298ccea… status 202 task task-f4b209aa… req req-954acd10…
    T16 rid2 状态: draft  rid2 任务数: 0
    [FAIL] T16 第二个需求未静默复用他人任务
    ```
  - 该现象也是我第一次（在被污染的遗留库上）跑仓库脚本时 `S5 同请求编号只创建一个生成任务 tasks=0`、`S5 已有处理中任务时再次发起被拒 status=202` 两项失败的根因。
- **建议修法**：幂等命中时校验 `existing.RequirementID == requirementID`，不一致返回 409；DB 唯一索引改为 `(requirement_id, request_no)` 或在服务层显式限定作用域。

### D-05（中）仓库自带场景脚本在平台默认参数下无法通过
- **触发条件**：按 `scripts/verify_scenarios.py` 文件头给出的用法（`--base` / `--mock`），平台按默认 `-clock-scale 1` 启动。
- **期望**：脚本应能自证通过。
- **实际**：`[FAIL] S3 第三次未成功后达到次数上限 status=generating`，`exit=1` —— S3 只等 25 秒（`scripts/verify_scenarios.py:193` `wait_for(..., timeout=25)`），而默认超时上限是题面规定的 60 秒。
- **证据**：全新库 + 默认参数实例实测 `通过 35 项，失败 1 项`；同一脚本在 `-clock-scale 0.1` 下 36/36 通过。脚本用法注释（`scripts/verify_scenarios.py:6-9`）未提示必须使用 0.1 倍率。
- **建议修法**：脚本按题面 60 秒口径等待（≥65s），或在文件头与 CI 中明确写死并说明 `-clock-scale 0.1` 的前置条件。

### D-06（中）文档泄漏明文演示口令，且自编 Skill 的泄密扫描规则漏检导致假 PASS
- **触发条件**：阅读 `docs/01-需求分析.md` 假设清单 A4。
- **期望**：`AGENTS.md` §5 第 4 条与 §8 第 3 条要求「演示口令的完整值只保留在 `cmd/server/main.go`，文档与证据一律以掩码展示」；`docs/04-部署与使用说明.md:45-51` 也确实用 `Stu****2026` 掩码。
- **实际**：`docs/01-需求分析.md:163` 明文写有 ``预置账号口令统一为 `Stu****2026` ``。更严重的是 `skills/acceptance-audit/scripts/acceptance_audit.py:23-29,327-344` 的「明文口令」正则要求 `口令[:=]` 形态，对「口令统一为 `Stu****2026`」**不命中**，于是 C-11 判 **PASS** 并在 `evidence/acceptance-audit/检查项明细.md` 留下「全量扫描 evidence/ 与 docs/ 未命中（未使用白名单或例外目录）」的结论 —— 该结论与事实不符。
- **证据**（我的独立全量扫描，35 个文件，未使用白名单/例外目录）：
  ```
  命中数: 1
   - docs\01-需求分析.md:163 | 演示口令明文 | | A4 | 预置账号口令统一为 `Stu****2026`，文档只展示掩码 | …
  ```
  9 类形态（ghp_/ghu_/github_pat_/Bearer/Authorization/私钥/口令赋值/密钥赋值/Cookie 值）其余均未命中；`evidence/github-mcp/*.json` 中的令牌已脱敏为 `ghp_****`。
  另：`scripts/verify_scenarios.py:20` 与 `internal/httpapi/*_test.go` 也含明文口令（AGENTS 要求完整值只在 `cmd/server/main.go`）。
- **建议修法**：A4 行改为掩码；把泄密正则改为「口令/密钥/令牌关键词 + 附近 40 字符内的强特征串」或直接维护「已知演示口令值」黑名单式检测（检测已知凭据值，而非依赖书写格式）；修正后重跑 Skill 并按 AGENTS §9 记录「发现问题→修正→复检」。

### D-07（低）自编 Skill `material-guard` 对 R7 无效，在含 D-01/D-02 的库上仍判 PASS
- **触发条件**：对含「撤销需求被复活为 `generating`/`failed`」的数据库运行该 Skill。
- **期望**：按 `AGENTS.md` §9，material-guard 应检查「撤销不复活」不变量。
- **实际**：**判定 PASS，exit 0**，8 条数据库不变量与 5 条源码一致性检查全绿。
- **证据**：
  ```
  -判定：**PASS**
  | G7 | R7 | 撤销的需求没有候选也没有采用记录 | PASS | 带候选的撤销需求：[]；撤销后仍被采用的：[] |
  | S3-CanStartGeneration | CanStartGeneration 的允许状态集合符合题面 | PASS | internal/domain/rules.go CanStartGeneration：缺少 无 |
  ```
  根因：G1–G8 只检查「撤销需求的候选/采用记录」，**不检查撤销需求是否被改写状态或被再次发起**；S3 只核对 `internal/domain/rules.go` 里 `CanStartGeneration` 的**声明**，不检查 service 层是否调用它（正是 D-01 的盲区）。另 `material_guard.py:204` 把覆盖率「待补证」时整体仍判 PASS。
- **建议修法**：新增不变量「状态为 `withdrawn` 的需求，其 `updated_at` 之后不得出现 `generation_started` / `generation_timeout` 事件，状态不得变为 `generating`/`failed`/`exhausted`」；S3 增查 `internal/service/requirement.go` 中是否出现 `CanStartGeneration(` 调用。

### D-08（低）`cmd/mockgen` 单测硬编码端口，存在偶发失败
- **触发条件**：两次测试运行并行（如 CI矩阵或两个会话同时跑 `go test ./...`）。
- **实际**：`--- FAIL: TestRunStartsAndStops ... listen tcp 127.0.0.1:18099: bind: Only one usage of each socket address ...`；端口释放后单独重跑 PASS。
- **证据**：`cmd/mockgen/main_test.go:185` 使用固定端口 `127.0.0.1:18099`；我的一次全量重跑 FAIL、单独重跑 PASS。
- **建议修法**：改用 `127.0.0.1:0` 动态端口。

### D-09（流程，低）交付物与远端证据- `evidence/` 中**没有** `evidence/deployment-main.log`（我运行 acceptance-audit 时该项被判「待补证」C-12），`docs/` 中虽有 `10-交付与发布说明.md`、`evidence/github-mcp/*` 记录，但**自动部署与流水线冒烟在本机无法证实**，且 `evidence/acceptance-audit/acceptance-audit.json` 等被提交进仓库，缺少「部署后运行实例」的独立佐证。此项本报告只作事实陈述，不判定通过与否（见 §4）。

---

## 4. 未覆盖 / 无法验证的范围

1. **`go test ./... -race`（竞态检测）未能执行** —— 环境边界：
   - `go test ./... -race` → `go: -race requires cgo; enable cgo by setting CGO_ENABLED=1`
   - `CGO_ENABLED=1 go test ./... -race` → `cgo: C compiler "gcc" not found: exec: "gcc": executable file not found in %PATH%`（`gcc/clang/cc` 均不存在）
   - `docker version` → `failed to connect to the docker API at npipe:////./pipe/dockerDesktopLinuxEngine`（Docker Desktop 守护进程未运行），无法按 `AGENTS.md` §7 第 4 条改用官方 golang 容器执行。
   - 因此「并发唯一性/竞态」仅有非 `-race` 的功能级验证（T1 通过），**未做数据竞争检测**。
2. **GitHub 远端链路未验证**：仓库建仓、PR合并、流水线状态检查、自动部署与线上冒烟，需要 GitHub 访问与凭据，本机不具备；`evidence/deployment-main.log` 缺失，我无法独立证实「已部署成功」。相关文档结论本报告不予采信。
3. **HTML 页面通道的浏览器端到端交互未做**（仅接口级验证 + 模板/表单处理函数走查）。D-03 在表单通道（`formWithdraw` 不 `ParseForm`）的后果由代码走查与同类 API 复现推断，未在真实浏览器复现。
4. **压力/长稳**：仅做了约 250 次写请求规模的连接重置统计（5/40、9/60 样本），未做长时间压测。
5. **并发发起同一 request_no 的线程级竞争**：仅从代码与单测层面核对（`AGENTS.md` §7 第 3 条要求），未在 `-race` 下验证。
6. 测试期间**另一会话在并发修改并提交本仓库**（HEAD 由 `8769b79` 变为 `621c85f`）。我以最终工作区重新编译并复测了 D-01/D-02/D-03；D-04/D-05/D-06/D-07 涉及的源文件在最终 HEAD 未变更。

---

## 5. 测试结论

**结论：不通过（FAIL）。**

- 缺陷统计：**严重 2 条（D-01、D-02）、中 4 条（D-03、D-04、D-05、D-06）、低 3 条（D-07、D-08、D-09）**。
- 关键业务红线：题面 R7「撤销后不得恢复需求」被 **2 条独立缺陷**同时破坏（发起侧 D-01、巡检侧 D-02），并直接违反 `AGENTS.md` §4 状态表；R2 幂等在跨需求场景存在串号（D-04）。按共同要求「不得判定整题通过的七条红线」第 1 条（关键业务验收不通过），**本题不应判为通过**。
- 通过项：gofmt 零差异、`go vet` 无告警、`go test ./... -count=1` 全绿、覆盖率 **整体 85.74%**，核心包 domain 89.71% / store 86.46% / service 85.43% / httpapi 89.69% / supplier 87.10%，**均 ≥85%（门禁 PASS）**；仓库自带场景脚本在 `-clock-scale 0.1` 下 36/36 通过；正常交付、重做、失败、迟到回调、重复回调、签名校验、权限隔离、重启持久化等分支均实测通过。
- 反作弊核查：无删除/弱化测试、无 `t.Skip`、覆盖率脚本无排除项、无泄密扫描白名单代码（但扫描**规则**漏检，见 D-06）。
- 修复优先级建议：**D-01 → D-02 → D-03 → D-04 → D-06 → D-05 → D-07 → D-08**。

---

## 我实际执行过的命令清单

```powershell
# 版本与环境
go version ; python --version ; git log --oneline -15 ; git rev-parse HEAD ; git status --porcelain
git diff --stat 8769b79 621c85f ; git diff --name-only 8769b79 621c85f ; git show621c85f:skills/acceptance-audit/scripts/acceptance_audit.py

# 静态与单元测试（仓库根目录）
gofmt -l .
go vet ./...
go test ./... -count=1
go test ./... -race -count=1                      # 失败：-race requires cgo
$env:CGO_ENABLED=1; go test ./... -race -count=1 # 失败：gcc not found
go test ./cmd/mockgen/ -count=1 -run TestRunStartsAndStops -v

# 覆盖率门禁（输出到临时目录，未写仓库）
python scripts/coverage_gate.py --root . --out-dir $env:TEMP\d6-cov  --min 85
python scripts/coverage_gate.py --root . --out-dir $env:TEMP\d6-cov2 --min 85

# 构建与运行（全部在 %TEMP%，仓库零写入）
go build -C <repo> -o $env:TEMP\d6bin\server.exe  ./cmd/server
go build -C <repo> -o $env:TEMP\d6bin\mockgen.exe ./cmd/mockgen
go build -C <repo> -o $env:TEMP\d6bin2\server.exe ./cmd/server
Start-Process server.exe  -addr :18180 -db $env:TEMP\ind-A.db -supplier-url http://127.0.0.1:18190 -clock-scale 0.1  -sweep-interval 2s
Start-Process server.exe  -addr :18280 -db $env:TEMP\ind-B.db -supplier-url http://127.0.0.1:18290 -clock-scale 0.1  -sweep-interval 3600s
Start-Process server.exe  -addr :18380 -db $env:TEMP\ind-C.db -supplier-url http://127.0.0.1:18390 -clock-scale 0.1  -sweep-interval 2s
Start-Process server.exe  -addr :18480 -db $env:TEMP\ind-D.db -supplier-url http://127.0.0.1:18490 -clock-scale 0.1  -sweep-interval 2s
Start-Process server.exe  -addr :18580 -db $env:TEMP\ind-E.db -supplier-url http://127.0.0.1:18590   # 默认 clock-scale=1
Start-Process mockgen.exe -addr :18190/:18290/:18390/:18490/:18590 -callback-url http://127.0.0.1:<port>/api/supplier/callback

# 场景脚本（仓库自带）
python scripts/verify_scenarios.py --base http://127.0.0.1:18180 --mock http://127.0.0.1:18190   # clock-scale 0.1 → 36/36, exit 0
python scripts/verify_scenarios.py --base http://127.0.0.1:18580 --mock http://127.0.0.1:18590   # 默认 1     → 35/36, exit 1

# 自编 Skill（输出到临时目录）
python skills/material-guard/scripts/material_guard.py --repo <repo> --db $env:TEMP\ind-C.db --out $env:TEMP\d6-mg\mg.md --skip-tests
python skills/acceptance-audit/scripts/acceptance_audit.py --repo <repo> --out-dir $env:TEMP\d6-aa --skip-toolchain

# 我自写的独立探针脚本（均在 %TEMP%，共 9 个）
python $env:TEMP\d6probe.py    # T1–T12 幂等/拒绝/超时/上限/迟到成功失败/历史候选/越权/重复回调/签名/未知任务
python $env:TEMP\d6probe2.py   # T13/T14/T15/T16 巡检与撤销/已交付撤销/未知任务回调/跨需求 request_no
python $env:TEMP\d6probe3.py   # 同上（重跑）
python $env:TEMP\d6probe4.py   # U1–U4 巡检复活撤销/撤销后再发起
python $env:TEMP\d6loop.py  ; python $env:TEMP\d6loopB.py   # 30 次写请求连接重置复现（两实例）
python $env:TEMP\d6raw.py ; python $env:TEMP\d6raw2.py ; python $env:TEMP\d6raw3.py ; python $env:TEMP\d6raw4.py  # 裸 socket 对照
python $env:TEMP\d6iso.py  ; python $env:TEMP\d6env.py      # 只读端点/mockgen 120 次对照
python $env:TEMP\d6body.py                                       # 带体 vs 不带体对照
python $env:TEMP\d6rst.py ; python $env:TEMP\d6before.py ; python $env:TEMP\d6after.py  # 重置时业务是否已生效 / 重启持久化
python $env:TEMP\d6verify.py                                     # V1/V2/V3 最终树复测
python $env:TEMP\d6secret.py                                     # docs/ 与 evidence/ 全量泄密扫描（无白名单）
python $env:TEMP\d6db.py                                         # SQLite 数据核对
Get-NetTCPConnection -State Listen -LocalPort 8080,8090,18080..18590 ; Stop-Process -Id <测试进程>
```