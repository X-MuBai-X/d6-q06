# 覆盖率缺口登记（internal/httpapi、internal/service、internal/store、internal/supplier）

本文件登记四个包中**确实无法用确定性单元测试覆盖**的代码块。
登记格式：源码位置 → 无法覆盖的原因 → 对覆盖率的影响（语句数/块数）→ 已做的替代验证。

> 说明：登记项均**没有删除任何测试**，也没有在 `scripts/coverage_gate.py` 中添加任何排除项；
> 门禁脚本与生产代码本次未做任何修改。

## 一、本次补测前后的对比（本机实测，`scripts/coverage_gate.py` 输出）

| 包 | 块口径（修改前） | 块口径（修改后） | 语句口径（修改前） | 语句口径（修改后） |
| --- | --- | --- | --- | --- |
| `internal/httpapi` | 85.66%（221/258） | **98.45%（254/258）** | 84.26%（CI 口径） | **98.48%（CI 口径）** |
| `internal/service` | 81.59%（297/364） | **97.25%（354/364）** | 86.76% | **98.02%** |
| `internal/store` | 82.03%（210/256） | **99.61%（255/256）** | 86.80% | **99.72%** |
| `internal/supplier` | 81.82%（18/22） | **95.45%（21/22）** | 87.10% | **96.77%** |
| 整体 | 82.86%（870/1050） | **96.00%（1008/1050）** | 86.05% | **95.16%** |

## 二、已登记的未覆盖块（共 16 块 / 16 条语句）

### A1. `internal/httpapi/server.go:41` —— `embed` 模板解析失败

- **无法覆盖的原因**：`html/template` 的模板文本由 `//go:embed` 在编译期写入二进制，
  单元测试无法替换 `templates` 变量；编译期能通过 `Parse` 的模板在运行时不会解析失败。
- **影响**：1 个语句 / 1 个块（`internal/httpapi` 块口径影响约 0.39%）。
- **替代验证**：已覆盖渲染路径的**运行期失败**分支——模板执行出错时返回 500「页面渲染失败」
  且不透出内部细节（`internal/httpapi/coverage_gap_test.go` 的 `TestRenderReturns500WhenTemplateFails`），
  并覆盖了模板成功渲染的全部页面（登录、工作台、需求详情、运营总览、异常列表）。

### A2. `internal/httpapi/server.go:265` —— `newRequestID` 随机源失败回退

- **无法覆盖的原因**：依赖 `crypto/rand` 读取操作系统熵源失败，属于操作系统级故障，
  标准库未提供注入失败点的接口。
- **影响**：1 个语句 / 1 个块。
- **替代验证**：已验证正常路径——每次请求生成的 `X-Request-ID` 互不相同，
  且在响应头与业务日志中一致出现（`TestRequestIDIsUniquePerRequest`）。

### A3. `internal/httpapi/web.go:170` / `web.go:175` —— 运营页异常查询 / 需求列表读取失败

- **无法覆盖的原因**：运营页在渲染前先调用 `Overview` 执行同一组存储查询；
  要让页面层的第二次查询单独失败，必须让存储层支持按调用次序注入错误，
  属于改动生产代码（引入可注入查询层）才能实现，超出本次补测范围。
- **影响**：2 个语句 / 2 个块。
- **替代验证**：已覆盖**同一数据来源**在服务层的失败分支——异常查询失败必须报错
  （`TestGapOverviewWhenEventTableUnavailable`）、需求列表失败必须报错
  （`TestUnknownRoleAccountCannotListRequirements`），并覆盖了运营页正常渲染
  （异常列表 + 需求列表）与工作台页的越权收口。

## 三、已登记的未覆盖块（service / store / supplier，共 12 块 / 12 条语句）

### 1. `internal/service/auth.go:29-30` —— 生成会话令牌时随机源失败

```go
buf := make([]byte, 24)
if _, err := rand.Read(buf); err != nil {
    return "", nil, fmt.Errorf("生成会话令牌失败: %w", err)
}
```

- **无法覆盖的原因**：`crypto/rand.Read` 读取操作系统熵源，失败属于操作系统级故障，
  Go 标准库没有提供注入随机源失败点的接口；本机熵源正常时该分支不会进入。
- **影响**：1 个语句 / 1 个块（`internal/service` 块口径影响约 0.27%）。
- **替代验证**：已覆盖同一函数的其余路径——空账号/空口令返回 `ErrInvalidInput`、
  账号不存在或口令错误返回 `ErrUnauthorized`、会话写入失败时登录必须报错且不返回令牌
  （见 `internal/service/coverage_gap_test.go` 的 `TestGapLoginWhenAccountTableUnavailable`、
  `TestGapLoginWhenSessionCannotBeStored`）。即「登录失败的三条可达路径」均已验证。

### 2. `internal/service/service.go:65-66` —— 生成业务 ID 时随机源失败的兜底

```go
if _, err := rand.Read(buf); err != nil {
    // 随机源不可用属于极端情况，回退到时间戳，保证 ID 仍然唯一。
    return fmt.Sprintf("%s-%d", prefix, s.now().UnixNano())
}
```

- **无法覆盖的原因**：同第 1 项，依赖 `crypto/rand` 故障，无法在单元测试中稳定注入。
- **影响**：1 个语句 / 1 个块（`internal/service` 块口径影响约 0.27%）。
- **替代验证**：已验证 ID 生成的正常路径——`CreateRequirement` / `StartGeneration` /
  `HandleCallback`（生成候选）产出的 ID 均带业务前缀且互不相同，
  并被正确写入 `requirements.id`、`generation_tasks.id`、`candidates.id`。

### 3. `internal/service/callback.go:190-193` —— 超时巡检中任务行已被并发删除

```go
current, err := s.store.GetTaskTx(ctx, tx, task.ID)
if err != nil {
    if errors.Is(err, domain.ErrNotFound) {
        return nil
    }
    return err
}
```

- **无法覆盖的原因**：任务清单 `ListProcessingTasksBefore` 与事务内 `GetTaskTx` 之间
  没有可注入的回调点；要进入该分支必须由**另一个连接在两次读之间删除任务行**，
  属于并发时序依赖，单元测试无法确定性复现（强行用 goroutine 竞争会产生偶发失败）。
- **影响**：3 个语句 / 3 个块（`internal/service` 块口径影响约 0.82%）。
- **替代验证**：已覆盖同一巡检循环中所有确定性失败分支——任务写回失败、需求不可读、
  需求状态写回失败、超时留痕失败、撤销需求的迟到超时留痕失败（对应
  `TestGapSweepTimeoutWhenTaskUpdateFails`、`TestGapSweepTimeoutWhenRequirementUnavailable`、
  `TestGapSweepTimeoutWhenRequirementUpdateFails`、`TestGapSweepTimeoutWhenTimeoutAuditFails`、
  `TestGapSweepTimeoutOnWithdrawnRequirementWhenAuditFails`），
  并验证了「超时判定 → 任务判为 timeout、需求置为 failed、留痕 generation_timeout」的完整业务链路
  （既有 `TestStartTimeoutSweeper`、`TestRegressionSweepTimeoutsKeepsWithdrawnRequirement`）。

### 4. `internal/service/callback.go:196-197` —— 巡检时任务已不是处理中

```go
if current.Status != domain.TaskProcessing {
    return nil
}
```

- **无法覆盖的原因**：清单查询的条件是 `status = 'processing'`，事务内复读的是**同一行**，
  在没有并发写入的情况下两者必然一致；要进入该分支必须由另一个连接在两次读之间
  把任务改成非处理中，同样是并发时序依赖。
- **影响**：1 个语句 / 1 个块（`internal/service` 块口径影响约 0.27%）。
- **替代验证**：已验证「已经是终态的任务不被巡检改写」这一业务结论——
  终态任务不进入超时清单（`TestGapSweepTimeoutWhenTaskUpdateFails` 中任务状态断言），
  以及终态任务收到迟到回调时被忽略并留痕（既有 `TestHandleCallbackConflictOnFinishedTask`）。

### 5. `internal/service/requirement.go:121-122` —— 幂等键跨需求校验时任务表读取失败

```go
if other, err := s.store.GetTaskByRequestNoTx(ctx, tx, requestNo); err == nil {
    return fmt.Errorf("%w: 该请求编号已属于需求 %s，不能跨需求复用", domain.ErrConflict, other.RequirementID)
} else if !errors.Is(err, domain.ErrNotFound) {
    return err
}
```

- **无法覆盖的原因**：发起生成流程中三次任务查询（按需求+请求编号、按请求编号、
  查找处理中任务）都命中同一张 `generation_tasks` 表。要让第 2 次查询失败而第 1 次成功，
  需要「表可读但某条 WHERE 条件报错」——SQLite 不支持在 SELECT 上注入按条件触发的错误；
  用触发器（仅对 INSERT/UPDATE/DELETE 生效）也无法命中 SELECT。
  只能通过改生产代码（引入可注入的查询层）实现，超出本次任务范围。
- **影响**：1 个语句 / 1 个块（`internal/service` 块口径影响约 0.27%）。
- **替代验证**：已覆盖同一分支的**业务判定本身**——跨需求复用同一请求编号被明确拒绝为
  `ErrConflict` 并指出占用它的需求编号（既有 `TestRegressionStartGenerationCrossRequirementRequestNo`）；
  也覆盖了第 1 次查询失败（任务表不可读）时必须报错、不得重复提交供应商
  （`TestGapStartGenerationWhenTaskTableUnavailable`）。

### 6. `internal/service/requirement.go:127-128` —— 查找处理中任务时读取失败

```go
if _, err := s.store.FindProcessingTaskTx(ctx, tx, requirementID); err == nil {
    return fmt.Errorf("%w: 该需求已有处理中的生成任务", domain.ErrConflict)
} else if !errors.Is(err, domain.ErrNotFound) {
    return err
}
```

- **无法覆盖的原因**：同第 5 项，三次任务查询命中同一张表，无法只让第 3 次查询失败。
- **影响**：1 个语句 / 1 个块（`internal/service` 块口径影响约 0.27%）。
- **替代验证**：已覆盖该分支的业务判定——同一需求已有处理中任务时再次发起必须返回
  `ErrConflict` 且不调用供应商（既有 `TestStartGenerationIdempotencyAndConflict`）。

### 7. `internal/service/requirement.go:264-265` —— 「当前候选」复核时命中历史候选

```go
cand, err = s.store.GetCurrentCandidateTx(ctx, tx, requirementID)
...
if !cand.IsCurrent {
    return fmt.Errorf("%w: 该候选已是历史版本，不能复核", domain.ErrNotCurrentCandidate)
}
```

- **无法覆盖的原因**：`GetCurrentCandidateTx` 的查询条件是 `is_current = 1`，
  返回的候选必然 `IsCurrent == true`，该分支在当前实现下不可达
  （历史候选会先在 `GetCandidateTx` 分支被拒绝，见 `requirement.go:252-254`）。
- **影响**：1 个语句 / 1 个块（`internal/service` 块口径影响约 0.27%）。
- **替代验证**：已验证业务结论——按候选编号复核历史候选被明确拒绝为
  `ErrNotCurrentCandidate`（既有 `TestReviewCandidate`），以及确认采用历史候选同样被拒绝
  （`TestGapAdoptWithHistoricalCandidate`）。

### 8. `internal/service/query.go:121-122` —— 运营总览读取最近事件失败

```go
recent, err := s.store.ListRecentEvents(ctx, 20)
if err != nil {
    return nil, err
}
```

- **无法覆盖的原因**：`ListAnomalyEvents` 与 `ListRecentEvents` 读的是同一张 `events` 表，
  总览先查异常再查最近事件；把 `events` 表破坏掉时**先**在 `query.go:117` 处失败
  （该分支已覆盖），无法让第二次查询单独失败。SQLite 不支持按查询形态注入错误。
- **影响**：1 个语句 / 1 个块（`internal/service` 块口径影响约 0.27%）。
- **替代验证**：已覆盖同函数的相邻分支——异常查询失败必须报错（`TestGapOverviewWhenEventTableUnavailable`），
  以及总览在任务不可读时必须报错（`TestGapOverviewWhenTaskTableUnavailable`）；
  并验证了总览正常路径下需求状态计数与任务状态计数正确（`TestGapOverviewCountsTasks`）。

### 9. `internal/store/store.go:35-36` —— 打开数据库时驱动未注册

```go
db, err := sql.Open("sqlite", dsn)
if err != nil {
    return nil, fmt.Errorf("打开数据库失败: %w", err)
}
```

- **无法覆盖的原因**：`sql.Open` 仅在**驱动未注册**时返回错误，而驱动名在 `Open` 中被硬编码为
  已注册的 `"sqlite"`（`modernc.org/sqlite`）；`database/sql` 不允许重复注册同名驱动，
  因此无法在测试中构造「驱动缺失」的状态，除非改动生产代码。
- **影响**：1 个语句 / 1 个块（`internal/store` 块口径影响约 0.39%）。
- **替代验证**：已覆盖 `Open` 的其余全部失败路径——连接不可用
  （`打开数据库失败` / `连接数据库失败`）与**建表失败**（`TestGapStoreSchemaMigrationFailure`：
  预置一个缺少 `owner_id` 列的旧库，`Open` 必须返回「初始化表结构失败」并关闭连接）；
  以及事务开启失败（`开启事务失败`）与提交失败（`提交事务失败`，
  `TestGapStoreTransactionBeginAndCommitFailures`）。

### 10. `internal/supplier/client.go:58-59` —— 提交请求序列化失败

```go
body, err := json.Marshal(req)
if err != nil {
    return SubmitResponse{}, fmt.Errorf("序列化提交请求失败: %w", err)
}
```

- **无法覆盖的原因**：`SubmitRequest` 只有两个 `string` 字段，`encoding/json` 对字符串永不失败，
  该分支不可达（除非给结构体加上不可序列化的字段类型，那就改变了生产模型）。
- **影响**：1 个语句 / 1 个块（`internal/supplier` 块口径影响约 4.55%）。
- **替代验证**：已覆盖 `Submit` 的其余全部失败分支——构造请求失败（非法基础地址）、
  调用供应商失败（网络不可达）、读取响应失败（响应体被截断）、状态码非 200/202、
  响应不是合法 JSON（见 `internal/supplier/coverage_gap_test.go`），
  并验证了供应商实际收到的 `ref` / `prompt` 与提交内容一致（既有 `TestSubmitAccepted`）。

## 四、覆盖策略说明

- 本次补测对 `internal/store` 采用「**关闭底层连接 + 直接以已关闭连接作为 `Execer`**」的方式，
  一次覆盖持久化层各接口的错误分支；对 `internal/service` 采用
  「**SQLite 触发器注入写库故障 + 删除表制造读库故障 + 供应商 HTTP 处理器注入环境故障**」，
  覆盖业务层的真实失败分支。
- 所有失败场景都断言了**可观察的业务结果**（返回错误类型、事务回滚后的落库内容、
  审计事件种类、需求状态机结果），没有无意义断言，也没有 `t.Skip`。
- 既有测试文件未做任何修改或弱化；新增文件仅四个：
  `internal/httpapi/coverage_gap_test.go`、`internal/service/coverage_gap_test.go`、
  `internal/store/coverage_gap_test.go`、`internal/supplier/coverage_gap_test.go`。
