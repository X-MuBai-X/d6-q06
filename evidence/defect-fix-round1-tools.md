# 缺陷修复留档 · 第一轮（工具侧）：D-03 / D-05 / D-08 + D-06 测试口令

> 来源：`evidence/independent-test-report.md` 的 D-03、D-05、D-08 三节（独立测试 Agent 在全新上下文复测得到）。
> 本轮只修这 3 个缺陷，并顺带处理 D-06 中「测试代码与脚本不得出现演示口令明文」这一小块。
> 修复范围：`internal/httpapi/**`、`cmd/**`、`scripts/verify_scenarios.py`。
> 未触碰：`internal/service/**`、`internal/store/**`、`internal/domain/**`、`skills/**`、`docs/**`（其它并行任务在改）。

---

## D-03（中）撤销接口不读取请求体，客户端约 5–15% 概率收到 TCP 连接重置

### 1. 现象

- `POST /api/requirements/{id}/withdraw` 携带请求体（如 `{}`）时，服务端业务已生效（状态确实变成 `withdrawn`），
  但客户端偶发 `ConnectionResetError [WinError 10054]`，服务端日志无任何记录。
- 统计证据（独立测试报告）：带请求体 9/60、5/40 次重置；不带请求体 0/60；只读端点与 mockgen 120 次 POST 均 0 次。
- 连带影响：仓库自带 `scripts/verify_scenarios.py` 第 214 行正是带 `{}` 调撤销，脚本会在该处抛 `ConnectionResetError` 整脚本中断。

### 2. 根因

- `internal/httpapi/api.go` 的 `apiWithdraw` 直接调用业务层，**从不读取请求体**；
  `internal/httpapi/web.go` 的 `formWithdraw` 也**未调用 `r.ParseForm()`**。
- Go 的 `net/http` 在请求体未被读完时关闭连接会向客户端发送 TCP 复位，客户端可能因此丢失已经生成的响应。
- 其余 JSON 处理器都走 `decodeJSON`（会读完请求体），因此不复现。

### 3. 改动（文件:行号）

| 文件:行号 | 改动 |
|---|---|
| `internal/httpapi/api.go:20-30` | 新增 `drainBody(r)`：把请求体读干净（`io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20))`） |
| `internal/httpapi/api.go:194-195` | `apiWithdraw` 处理业务前先 `drainBody(r)` |
| `internal/httpapi/web.go:267-273` | `formWithdraw` 先 `r.ParseForm()`（失败返回 400） |
| `internal/httpapi/web.go:89-94` | `pageLogout`（`POST /logout`）新增 `r.ParseForm()`——原实现同样不读请求体 |
| `internal/httpapi/web.go:202-207` | `formUpdateDescription` 显式补 `r.ParseForm()`（原先只靠 `r.PostFormValue` 的隐式解析） |
| `internal/httpapi/web.go:243-248` | `formAdopt` 显式补 `r.ParseForm()`（同上） |
| `internal/httpapi/server.go:91` | 路由出口改为 `s.recoverMiddleware(s.bodyDrainMiddleware(mux))` |
| `internal/httpapi/server.go:94-102` | 新增 `bodyDrainMiddleware`：任何处理器返回后统一把请求体读干净（兜底，防止后续新增写接口重犯） |

### 4. 同包其它写接口检查清单与结论

| 处理器 | 位置 | 修复前是否读请求体 | 结论 |
|---|---|---|---|
| `apiLogin` | `api.go:44` | 是（`decodeJSON`） | 无需修改 |
| `apiCreateRequirement` | `api.go:74` | 是（`decodeJSON`） | 无需修改 |
| `apiUpdateDescription` | `api.go:105` | 是（`decodeJSON`） | 无需修改 |
| `apiStartGeneration` | `api.go:121` | 是（`decodeJSON`） | 无需修改 |
| `apiReview` | `api.go:141` | 是（`decodeJSON`） | 无需修改 |
| `apiAdopt` | `api.go:160` | 是（`decodeJSON`） | 无需修改 |
| `apiRework` | `api.go:177` | 是（`decodeJSON`） | 无需修改 |
| `apiOperatorNote` | `api.go:204` | 是（`decodeJSON`） | 无需修改 |
| **`apiWithdraw`** | `api.go:194` | **否** | **已修（`drainBody`）** |
| `apiSupplierCallback` | `server.go:223` | 是（`readBody`） | 无需修改 |
| `pageLoginSubmit` | `web.go:67` | 是（`ParseForm`） | 无需修改 |
| **`pageLogout`** | `web.go:89` | **否** | **已修（`ParseForm`）** |
| `formCreateRequirement` | `web.go:184` | 是（`ParseForm`） | 无需修改 |
| `formUpdateDescription` | `web.go:202` | 隐式（`PostFormValue`） | 已补显式 `ParseForm` |
| `formStartGeneration` | `web.go:213` | 是（`ParseForm`） | 无需修改 |
| `formReview` | `web.go:229` | 是（`ParseForm`） | 无需修改 |
| `formAdopt` | `web.go:243` | 隐式（`PostFormValue`） | 已补显式 `ParseForm` |
| `formRework` | `web.go:255` | 是（`ParseForm`） | 无需修改 |
| **`formWithdraw`** | `web.go:267` | **否** | **已修（`ParseForm`）** |
| `formOperatorNote` | `web.go:278` | 是（`ParseForm`） | 无需修改 |

结论：全包共 19 个写处理器，修复前有 3 个（`apiWithdraw`、`formWithdraw`、`pageLogout`）完全不读请求体，
2 个（`formUpdateDescription`、`formAdopt`）只靠 `PostFormValue` 的隐式解析（表单 `Content-Type` 缺失时不读体）。
现全部显式读体，并叠加 `bodyDrainMiddleware` 兜底。

### 5. 回归测试（新增，均可重复运行、不依赖真实网络）

新增文件 `internal/httpapi/httpapi_body_test.go`：

| 测试名 | 断言内容 |
|---|---|
| `TestAPIDrawerHandlerConsumesRequestBody` | 直接调用 `apiWithdraw`，用计数 Reader 断言请求体被读满（`{}` → 2 字节） |
| `TestFormWithdrawHandlerConsumesRequestBody` | 直接调用 `formWithdraw`，断言 `ParseForm` 读掉了请求体 |
| `TestWriteHandlersConsumeRequestBody` | 10 个写接口（创建/描述/生成×2/复核/采用/重做/撤销/运营意见/登录）逐个断言请求体被读满 |
| `TestWithdrawWithRequestBodyOverHTTPIsStable` | **连续 40 次带 `{}` 请求体调撤销**，每次都必须拿到 200 或 409，**不得出现连接错误**；末尾校验状态为 `withdrawn` |
| `TestWriteEndpointsWithRequestBodyOverHTTPIsStable` | 其余 7 个写接口 × 5 轮带体请求，断言无连接错误且状态码落在 2xx/4xx |

**回归有效性实测**（临时把 `apiWithdraw` 的 `drainBody(r)` 去掉后重跑）：

```
--- FAIL: TestAPIDrawerHandlerConsumesRequestBody (0.07s)
    httpapi_body_test.go:75: 撤销接口必须读干请求体，实际读取 0 字节
FAIL
```

即：修复前该用例必然失败，修复后通过（`0 字节` → `2 字节`）。

---

## D-05（中）仓库自带场景脚本在平台默认参数下无法通过

### 1. 现象

- `scripts/verify_scenarios.py` 在平台按默认 `-clock-scale 1`（题面真实 60 秒时限）启动时：`通过 35 项，失败 1 项`，
  失败项 `[FAIL] S3 第三次未成功后达到次数上限 status=generating`，退出码 1。
- 同一脚本在 `-clock-scale 0.1` 下 36/36 通过。脚本文件头的用法注释没有提示需要 0.1 倍率。

### 2. 根因

- 脚本第 193 行只等 25 秒（`wait_for(..., timeout=25)`），而默认超时上限是题面规定的 60 秒；
  平台的超时由巡检判定（默认巡检间隔 5 秒），最晚在 60 秒之后 5 秒内才置为 `exhausted`。

### 3. 修法选择与理由

选择 **方案 (1)：让脚本在题面真实口径（60 秒）下自证通过**。

- 理由：脚本是仓库自带的「自证」工具，如果它必须依赖教学时钟倍率才能通过，就等于把「演示便利」当成了「验收前提」；
  题面允许可配置教学时钟，但只改变时限长度、不改变判定。让脚本按 60 秒口径等待，
  任何倍率下都能跑通（倍率 0.1 时条件提前成立、脚本提前返回），脚本不再依赖倍率。
- **没有**删减或放宽 S3 的任何断言（仍是「第三次未成功后达到次数上限 status=exhausted」+「第四次发起被拒绝 409」），超时判定仍由平台按题面 60 秒口径完成。

### 4. 改动（文件:行号）

| 文件:行号 | 改动 |
|---|---|
| `scripts/verify_scenarios.py:41` | 新增 `TASK_TIMEOUT_SECONDS = 60`（题面 R3 口径，写死不随倍率变化） |
| `scripts/verify_scenarios.py:44` | 新增 `TASK_TIMEOUT_WAIT_SECONDS = TASK_TIMEOUT_SECONDS + 15`（60 秒时限 + 5 秒巡检间隔 + 余量） |
| `scripts/verify_scenarios.py:237` | S3 超时等待由 `timeout=25` 改为 `timeout=TASK_TIMEOUT_WAIT_SECONDS` |
| `scripts/verify_scenarios.py:3-27` | 文件头补充「教学时钟」与「预置账号口令」两节前置说明 |
| `scripts/verify_scenarios.py:330-336` | `--help` 增加 `epilog`，写明「默认按题面 60 秒口径等待（S3 最长约 75 秒）；若用 `-clock-scale 0.1`，条件成立即提前返回，断言不变」 |

---

## D-08（低）`cmd/mockgen` 单测硬编码端口，存在偶发失败

### 1. 现象

两次测试并行时 `cmd/mockgen TestRunStartsAndStops` 偶发
`listen tcp 127.0.0.1:18099: bind: Only one usage of each socket address`，单独重跑又 PASS。

### 2. 根因

`cmd/mockgen/main_test.go:185` 使用固定端口 `127.0.0.1:18099`。

### 3. 改动（文件:行号）

| 文件:行号 | 改动 |
|---|---|
| `cmd/mockgen/main.go:88-97` | `run` 拆出 `net.Listen` 步骤，监听失败直接返回错误 |
| `cmd/mockgen/main.go:99-133` | 新增 `serve(ctx, cfg, ln)`：在**已监听**的端口上提供模拟供应商服务（监听与业务分离，便于测试使用动态端口） |
| `cmd/mockgen/main_test.go:182-221` | `TestRunStartsAndStops` 改为 `net.Listen("tcp","127.0.0.1:0")` 取实际地址再 `serve`，并轮询等待健康检查（不再依赖 300ms 固定睡眠） |
| `cmd/mockgen/main_test.go:223-238` | 新增 `TestRunAcceptsDynamicPort`，覆盖 `run` 走 `127.0.0.1:0` 的启动路径 |
| `cmd/server/main_test.go:33-40` | 新增 `freeAddr(t)` 辅助函数（动态端口） |
| `cmd/server/main_test.go:43-108` | `TestSeedCreatesPresetAccounts` 改用动态端口并补充口令断言 |
| `cmd/server/main_test.go:110-137`、`:139-157`、`:159-181` | 其余 3 个用例的固定端口（18097/18095/18094）改为动态端口，同类偶发失败一并消除 |

测试未被删除、未被 `t.Skip`。

---

## D-06（部分）测试代码与脚本中的演示口令明文

> 只做「测试代码与脚本」这一小块；`cmd/server/main.go` 中的演示口令常量**未改动**，
> `docs/01-需求分析.md:163` 的明文属于其它任务范围（`docs/**` 本轮不可写）。

### 1. 改动（文件:行号）

| 文件:行号 | 改动 |
|---|---|
| `internal/httpapi/password_test.go:14`（新增） | 测试专用口令常量 `testPassword`（掩码值 `Test****2026`，与演示口令不同） |
| `internal/httpapi/password_test.go:19-33` | `demoPasswordFromSource()`：从 `../../cmd/server/main.go` 运行时解析演示口令，**测试代码不硬编码该明文** |
| `internal/httpapi/httpapi_test.go:59`、`:79`、`:184` | 原 `Stu****2026`（原演示口令明文，按 AGENTS.md §8 第 3 条掩码） 明文 → `testPassword` |
| `internal/httpapi/httpapi_flow_test.go:185` | 原 `Stu****2026`（原演示口令明文，按 AGENTS.md §8 第 3 条掩码） 明文 → `testPassword` |
| `cmd/server/main.go:35`、`:47`、`:76`、`:114` | 新增 `-seed-password` 启动参数（默认仍是 `demoPassword`），使平台可以用测试专用口令预置账号；`seed(svc, password)` 按参数预置 |
| `cmd/server/main_test.go:16` | 测试专用口令常量 `testSeedPassword`（掩码值 `Test****2026`） |
| `cmd/server/main_test.go:43-108` | 平台用测试口令预置后：测试口令登录 200、演示口令登录 401 |
| `scripts/verify_scenarios.py:38` | 脚本口令常量改为 `TEST_PASSWORD`（掩码值 `Test****2026`，不再是演示口令） |
| `scripts/verify_scenarios.py:50-62` | `demo_password_from_source()`：从 `cmd/server/main.go` 运行时读取演示口令，脚本内不含明文 |
| `scripts/verify_scenarios.py:345-347` | 断言测试口令 ≠ 演示口令，相等则直接失败退出（退出码 2） |
| `scripts/verify_scenarios.py:349-365` | 口令来源优先级：`--password` > 环境变量 `STUDIO_PASSWORD` > 脚本内置测试专用口令；若平台以默认演示口令启动，打印提示后回退到来源文件中的演示口令（保持 CI 冒烟可用） |

### 2. 证明「测试口令与演示口令不相同」的断言

- `internal/httpapi/password_test.go:36` `TestTestPasswordDiffersFromDemoPassword`：运行时读出演示口令并断言 `testPassword != 演示口令`。
- `internal/httpapi/password_test.go:50` `TestNoPlaintextDemoPasswordInPackageFiles`：扫描本包全部 `*.go`，断言**不再出现演示口令明文**。
- `cmd/server/main_test.go:19` `TestDemoPasswordDiffersFromTestSeedPassword`：断言 `testSeedPassword != demoPassword`。
- `scripts/verify_scenarios.py:345-347`：脚本启动即断言 `TEST_PASSWORD != 演示口令`。

> 说明：独立测试报告称 `cmd/mockgen` 与 `cmd/server` 的测试文件里也有该明文口令，
> 实测复核后**这两处并不存在**演示口令明文（`cmd/mockgen` 测试里只有供应商签名密钥 `test-secret`，
> 不是演示口令），故无需改动；明文实际只在 `internal/httpapi/*_test.go` 与 `scripts/verify_scenarios.py`，
> 已全部替换。`internal/service/service_test.go` 仍含明文，但该文件不在本轮可写范围。

---

## 复检（真实执行）

```
cd 仓库根
gofmt -l .                       → （空输出）
go vet ./...                     → （空输出）
go test ./... -count=1
  ok  creative-studio/cmd/mockgen      5.420s
  ok  creative-studio/cmd/server       1.389s
  ok  creative-studio/internal/domain  0.467s
  ok  creative-studio/internal/httpapi  1.099s
  ok  creative-studio/internal/service 1.609s
  ok  creative-studio/internal/store   2.770s
  ok  creative-studio/internal/supplier 3.789s

python scripts/coverage_gate.py --root . --out-dir evidence --min 85
  creative-studio/cmd/mockgen              80.65%  (125/155)
  creative-studio/cmd/server               56.34%  (40/71)
  creative-studio/internal/domain          89.71%  (61/68)
  creative-studio/internal/httpapi         89.51%  (529/591)
  creative-studio/internal/service         85.38%  (432/506)
  creative-studio/internal/store           86.24%  (307/356)
  creative-studio/internal/supplier        87.10%  (27/31)
  【整体覆盖率】 85.55%  (1521/1778)
  【结论】PASS（整体与各核心包均 ≥ 85.0%）   gate exit=0
```

### 默认时钟下的场景脚本实测

平台**不传 `-clock-scale`**（默认倍率 1），全新数据库，mockgen 指向平台回调：

```
2026/11/13 22:38:32 服务已启动，教学时钟倍率=1（超时上限 1m0s）
预置账号口令来源：脚本内置测试专用口令
...
[PASS] S3 第三次未成功后达到次数上限 status=exhausted
[PASS] S3 三次历史均可追溯
[PASS] S3 第四次发起被拒绝 status=409
...
通过 36 项，失败 0 项
=== 用时（秒）：67.1 ===
=== 脚本退出码：0 ===
```

- 完整日志：`evidence/scenario-verify-default-clock.log`。
- **结论：修复后默认时钟（`-clock-scale 1`）下场景脚本 36/36 全通过、退出码 0**（修复前为 35/36、退出码 1）。
- 兼容性复测：同一脚本在 `-clock-scale 0.1` 下仍为 **36/36 通过、退出码 0、用时 12.1 秒**（条件提前成立即返回，断言未变）。
