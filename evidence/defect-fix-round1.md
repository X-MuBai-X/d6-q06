# 缺陷修正与复检记录 · 第 1 轮（D-01 / D-02 / D-04）

- 依据：`evidence/independent-test-report.md`（独立测试 Agent 报告）中的 D-01、D-02、D-04 三节。
- 修正范围：仅 `internal/service/**`、`internal/store/**`、`internal/domain/**`（本轮未改动 `internal/httpapi`、`cmd/`、`scripts/`、`skills/`、`docs/`）。
- 缺陷报告里的其它缺陷（D-03、D-05～D-09）不在本轮范围内，未处理。

---

## D-01（严重）撤销后仍可发起生成，已撤销需求被复活为 `generating`

### 现象
需求撤销（`withdrawn`）后，用**新** `request_no` 发起生成返回 202，需求状态被改写为 `generating`，
并真的新建了生成任务、向供应商提交了任务。

### 根因
`internal/service/requirement.go` 的 `StartGeneration` 事务内只做了「归属校验 → 幂等键命中 →
是否已有 processing 任务 → 次数上限」四道校验，从未调用领域层已有的 `domain.CanStartGeneration`，
因此 `withdrawn` 状态完全没有被拦截。

### 改动
| 文件 | 行号 | 内容 |
|---|---|---|
| `internal/service/requirement.go` | 100–105 | 在事务内「归属校验之后」补上状态闸门：`if req.Status != domain.ReqGenerating && !domain.CanStartGeneration(req.Status) { return fmt.Errorf("%w: 当前状态 %s 不允许发起生成", domain.ErrInvalidState, req.Status) }`。`domain.ErrInvalidState` 在 `internal/httpapi/server.go:175` 已映射为 HTTP 409。`generating` 状态放行到原有的「已有处理中任务」冲突判定（`ErrConflict`，同为 409），以保持 R2 的既有口径与既有断言不变。 |
| `internal/service/requirement.go` | 106–122 | 幂等查找改为按 `(requirement_id, request_no)` 作用域命中（见 D-04），并新增跨需求复用的明确拒绝。 |

未删除、未放宽任何既有校验（归属、幂等、处理中任务冲突、次数上限全部保留）。

### 回归测试
`internal/service/regression_test.go`
- `TestRegressionStartGenerationAfterWithdraw`：撤销后用新 `request_no` 发起 → 断言返回 `domain.ErrInvalidState`（409）、需求仍为 `withdrawn`、生成任务数仍为 0、供应商调用次数仍为 0。
- `TestRegressionStartGenerationAfterWithdrawWhileGenerating`：生成中撤销后再次发起 → 断言 `ErrInvalidState`、需求仍为 `withdrawn`、任务数仍为 1、供应商未被再次调用。
- `TestRegressionWithdrawnRequirementRejectsFurtherEdits`：撤销后不得修改描述、他人不得对其发起生成。

### 彻底解决？
是。撤销状态在服务层被显式拒绝，需求不会被复活，不会新增任务，不会调用供应商。
残余风险：`internal/httpapi/web.go` 的「发起生成」按钮是否显示由 `domain.CanStartGeneration` 控制，
与服务层闸门口径一致；页面通道同样会拿到 409（由其它并行任务负责 httpapi 侧，本轮未改动）。

---

## D-02（严重）超时巡检把已撤销需求复活为 `failed` / `exhausted`

### 现象
生成任务处于 `processing` 时撤销需求，随后超时巡检（`SweepTimeouts`）把需求状态由 `withdrawn`
改成 `failed`（或 `exhausted`），并写入 `generation_timeout` 事件。

### 根因
`internal/service/callback.go` 的 `SweepTimeouts` 拿到 `req` 后未判断 `req.Status == domain.ReqWithdrawn`，
直接执行 `UpdateRequirementStatusTx(..., NextStatusAfterAttemptFailure(...))`。

### 改动
| 文件 | 行号 | 内容 |
|---|---|---|
| `internal/service/callback.go` | 208–217 | 巡检命中已撤销需求时**只**把生成任务置为 `timeout`，并写入一条 `domain.EventLateResultIgnored`（仓库已有的「迟到结果被忽略」事件种类，语义等价，未臆造新命名）事件，**绝不改写需求状态**；事件文案为「需求已撤销，迟到的超时结果仅保留审计，未改写需求状态」。 |
| `internal/service/callback.go` | 173–175 | 更新函数注释，说明已撤销需求只记超时与审计、不得恢复。 |

该事件同时在 `domain.IsAnomalyEvent` 与 `service.AnomalyKinds` 的异常注册表内，运营可在异常记录中查到。

### 回归测试
`internal/service/regression_test.go`
- `TestRegressionSweepTimeoutsKeepsWithdrawnRequirement`：生成中撤销 → 把任务时间前移 → 巡检 → 断言需求仍为 `withdrawn`、任务状态为 `timeout`、事件中存在 `late_result_ignored`、且**不存在** `generation_timeout` 事件。
- `TestRegressionDetailViewExposesCurrentCandidate`：断言撤销后巡检留下的「迟到结果被忽略」能被运营在异常记录中查到（`ListAnomalies`）。

### 彻底解决？
是。撤销需求不会再被巡检改写状态，也不会再产生 `generation_timeout` 事件。
残余风险：供应商侧的真实任务仍会跑完（题面 R7 允许），其迟到回调继续走既有的「迟到结果只留审计」分支（`internal/service/callback.go:95`、`:125`），本次未改动。

---

## D-04（中）幂等键 `request_no` 未按需求隔离，跨需求复用会串号并静默不执行

### 现象
用已存在的 `request_no` 对**另一个需求**发起生成，返回 202，但返回的是**别的需求**的任务对象；
新需求仍是 `draft`、任务数 0、供应商也没被调用（一次「成功」的 202 却什么也没做）。

### 根因
`internal/service/requirement.go` 的 `StartGeneration` 用 `GetTaskByRequestNoTx` 全局按 `request_no`
命中即返回，未核对 `task.RequirementID == requirementID`；`internal/store/tasks.go`、
`internal/store/tx.go` 里按 `request_no` 查询的 SQL 也只按 `request_no` 过滤。

### 改动
| 文件 | 行号 | 内容 |
|---|---|---|
| `internal/store/tasks.go` | 40–47 | 新增 `GetTaskByRequirementRequestNo(ctx, requirementID, requestNo)`。 |
| `internal/store/tasks.go` | 125–138 | 新增内部实现 `getTaskByRequirementRequestNo`，SQL 条件为 `WHERE requirement_id = ? AND request_no = ?`（按需求限定作用域）。 |
| `internal/store/tx.go` | 75–81 | 新增 `GetTaskByRequirementRequestNoTx(ctx, q, requirementID, requestNo)`（事务内版本）。 |
| `internal/service/requirement.go` | 106–115 | 幂等命中改为**按需求作用域**查询：同一需求同一 `request_no` 才视为幂等重放（返回同一任务、不重复调用供应商）。 |
| `internal/service/requirement.go` | 116–122 | 作用域内未命中时，再按 `request_no` 全局查一次：若该编号已属于**其它需求**，返回 `domain.ErrConflict`（HTTP 409），错误信息带上占用它的需求 ID；不再返回他需求的任务对象，也不再「202 却什么也没做」。 |
| `internal/store/schema.sql` | 53–58 | DB 侧唯一索引按 `(requirement_id, request_no)` 收敛：`DROP INDEX IF EXISTS idx_task_request_no;` + `CREATE UNIQUE INDEX IF NOT EXISTS idx_task_requirement_request_no ON generation_tasks(requirement_id, request_no);`。 |

**索引改动说明（兼容既有库）**：`schema.sql` 是建库脚本，`store.Open` 每次打开数据库都会整段执行。
因此先 `DROP INDEX IF EXISTS` 旧的全局唯一索引、再建复合唯一索引：全新库与既有库都能得到同一结果；
复合约束比原约束更弱，既有数据不可能违反，无需数据迁移。跨需求复用的拦截由服务层的显式判定
（第 116–122 行）承担，复合索引则保证「同一需求同一请求编号」在数据库层仍不会重复落库。
原有唯一约束测试（`internal/store/store_test.go` 中「同一请求编号不允许重复」）在新索引下依然通过
（同一需求 + 同一请求编号仍违反复合唯一约束）。

### 回归测试
- `internal/service/regression_test.go`
  - `TestRegressionStartGenerationCrossRequirementRequestNo`：跨需求复用同一 `request_no` → 断言返回 `domain.ErrConflict`（409）、不返回任务对象、第二个需求仍为 `draft`、任务数 0、供应商调用次数仍为 1。
  - `TestRegressionStartGenerationIdempotencyWithinRequirement`：同一需求内幂等重放仍然返回同一任务、供应商只被调用 1 次（确认修复没有破坏 R2 幂等）。
- `internal/store/store_test.go`
  - `TestGetTaskByRequirementRequestNo`：按需求 + 请求编号读取；需求不匹配或编号不存在时返回 `ErrNotFound`。
  - `TestListRequirementsLimitAndAnomalyLimit`：列表查询上限与异常查询上限分支。

### 彻底解决？
是。跨需求复用被明确拒绝（409），不会再返回他需求的任务对象，也不会静默不执行。
残余风险：若绕过服务层直接写库（`store.InsertTask`），复合唯一索引允许不同需求使用相同 `request_no`；
该路径只有测试使用，生产写入唯一入口是 `StartGeneration`。

---

## 复检命令与真实输出摘要

复检目录：仓库根 `C:\Users\26765\Documents\trae_projects\skills\2组\素材\d6`

### 1. `gofmt -l .`
```
（空输出）  gofmt exit=0
```

### 2. `go vet ./...`
```
（空输出）  vet exit=0
```

### 3. `go test ./... -count=1`
```
ok  	creative-studio/cmd/mockgen	5.632s
ok  	creative-studio/cmd/server	8.062s
ok  	creative-studio/internal/domain	1.057s
ok  	creative-studio/internal/httpapi	1.265s
ok  	creative-studio/internal/service	1.876s
ok  	creative-studio/internal/store	3.371s
ok  	creative-studio/internal/supplier	4.261s
test exit=0
```

### 4. `python scripts/coverage_gate.py --root . --out-dir evidence --min 85`
```
creative-studio/cmd/mockgen          80.65%  (125/155)
creative-studio/cmd/server           56.34%  (40/71)
creative-studio/internal/domain      89.71%  (61/68)
creative-studio/internal/httpapi     89.51%  (529/591)
creative-studio/internal/service     86.76%  (439/506)
creative-studio/internal/store       86.80%  (309/356)
creative-studio/internal/supplier    87.10%  (27/31)
【整体覆盖率】 86.05%  (1530/1778)
【结论】PASS（整体与各核心包均 ≥ 85.0%）   gate exit=0
```
与独立测试报告中的基线（整体 85.74%、service 85.43%、store 86.46%）相比：**整体 86.05% ≥ 85.74%**、
**service 86.76% ≥ 85.43%**、**store 86.80% ≥ 86.46%**，均不低于此前水平。

### 5. 修复前复现（在仓库副本上回退生产代码，保留新测试）
把仓库复制到临时目录后，仅回退 `internal/service/requirement.go` 与 `internal/service/callback.go`
的生产代码（测试与 store 新方法保留），运行 `go test ./internal/service/ -run TestRegression -v`：
```
--- FAIL: TestRegressionStartGenerationAfterWithdraw          撤销后发起生成应返回 ErrInvalidState（映射 409），实际 <nil>
--- FAIL: TestRegressionStartGenerationAfterWithdrawWhileGenerating  撤销后再次发起应返回 ErrInvalidState，实际 操作冲突: 该需求已有处理中的生成任务
--- FAIL: TestRegressionSweepTimeoutsKeepsWithdrawnRequirement 撤销后的需求不得被巡检复活，实际 "failed"
--- FAIL: TestRegressionStartGenerationCrossRequirementRequestNo  跨需求复用请求编号应返回 ErrConflict（映射 409），实际 <nil>
--- FAIL: TestRegressionDetailViewExposesCurrentCandidate        撤销后的迟到结果应出现在运营异常记录中（实际只有 generation_timeout）
FAIL	creative-studio/internal/service
```
说明三个缺陷的回归测试在修复前确实失败、修复后通过，可独立复现。

---

## 附：本轮改动文件清单

| 文件 | 性质 |
|---|---|
| `internal/service/requirement.go` | D-01 状态闸门、D-04 幂等作用域与跨需求拒绝 |
| `internal/service/callback.go` | D-02 巡检不改写已撤销需求 |
| `internal/store/tasks.go` | 新增按需求 + 请求编号读取任务的方法 |
| `internal/store/tx.go` | 新增事务内版本 |
| `internal/store/schema.sql` | 唯一索引收敛为 `(requirement_id, request_no)` |
| `internal/service/regression_test.go` | 新增：8 个回归测试 |
| `internal/store/store_test.go` | 新增：2 个存储层测试 |
| `evidence/defect-fix-round1.md` | 本记录 |
