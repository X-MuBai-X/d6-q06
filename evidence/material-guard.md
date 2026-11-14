# material-guard 检查报告

- 判定：**PASS**
- 数据库：`C:\Users\26765\Documents\trae_projects\skills\2组\素材\d6\data\run9.db`
- 覆盖率门禁：PASS

## 一、数据库不变量
| 编号 | 规则 | 检查项 | 结论 | 证据 |
| --- | --- | --- | --- | --- |
| G1 | R1 | 成功任务的描述快照非空 | PASS | 检查 5 条成功任务；异常记录：无 |
| G2 | R2 | 同一需求至多一个处理中任务且请求编号唯一 | PASS | 重复处理中：[]；重复请求编号：[] |
| G3 | R3 | 生成次数不超过三次且失败类任务不产出候选 | PASS | 超限需求：[]；失败任务带候选：[] |
| G4 | R4/R7 | 成功任务恰好一个候选且当前候选唯一（撤销需求的迟到结果除外） | PASS | 多候选任务：[]；缺候选任务：[]；多当前候选需求：[] |
| G5 | R5 | 已确认采用的需求必须有复核通过的当前候选 | PASS | 异常需求：[] |
| G6 | R6 | 历史候选不再处于待复核状态 | PASS | 异常候选：[] |
| G7 | R7 | 撤销的需求没有候选也没有采用记录 | PASS | 带候选的撤销需求：[]；撤销后仍被采用的：[] |
| G8 | R8 | 需求与生成任务均有审计留痕 | PASS | 缺创建事件的需求：[]；缺事件的任务：[] |
| G9 | R7 | 撤销的需求不得复活（状态不得被改写，撤销后不得再出现推进事件） | PASS | 撤销后状态被改写的需求：[]；撤销后仍出现推进事件：[] |

## 二、源码一致性
| 编号 | 检查项 | 结论 | 证据 |
| --- | --- | --- | --- |
| S1 | 最大生成次数为 3 | PASS | internal/domain/rules.go MaxAttempts = 3 |
| S2 | 生成超时上限为 60 秒 | PASS | internal/domain/rules.go GenerationTimeout = 60 秒 |
| S3-CanStartGeneration | CanStartGeneration 的允许状态集合符合题面 | PASS | internal/domain/rules.go CanStartGeneration：缺少 无 |
| S3-CanReview | CanReview 的允许状态集合符合题面 | PASS | internal/domain/rules.go CanReview：缺少 无 |
| S3-CanAdopt | CanAdopt 的允许状态集合符合题面 | PASS | internal/domain/rules.go CanAdopt：缺少 无 |
| S3-CallCanStartGeneration | 业务层存在 CanStartGeneration 的调用点（不只看声明） | PASS | 调用点：['internal/service/requirement.go:67', 'internal/service/requirement.go:103'] |
| S3-CallInStartGeneration | StartGeneration 内调用 CanStartGeneration（撤销后不得再发起） | PASS | internal/service/requirement.go 的 StartGeneration：已调用 CanStartGeneration |

## 三、覆盖率
```
题目 06 单元测试覆盖率门禁报告
============================================================
覆盖率剖面：C:\Users\26765\Documents\trae_projects\skills\2组\素材\d6\evidence\coverage.out
统计口径：逐包执行 go test -coverprofile 后按块去重汇总
  · 语句口径：按剖面 numstmt 统计（题面要求的语句覆盖率）
  · 块口径：已覆盖代码块 / 总代码块，跨平台一致，作为保守下界
  · 两种口径同时达标才算通过（避免本机通过而 CI 失败）
排除项：无（自编的数据库访问与 HTTP 处理代码全部纳入统计）

【各包覆盖率】
  creative-studio/cmd/mockgen              语句  80.65%  块  80.65%  (125/155 ; 50/62)
  creative-studio/cmd/server               语句  56.34%  块  74.07%  (40/71 ; 20/27)
  creative-studio/internal/domain          语句  89.71%  块  88.52%  (61/68 ; 54/61)
  creative-studio/internal/httpapi         语句  98.98%  块  98.45%  (585/591 ; 254/258)
  creative-studio/internal/service         语句  98.02%  块  97.25%  (496/506 ; 354/364)
  creative-studio/internal/store           语句  99.72%  块  99.61%  (355/356 ; 255/256)
  creative-studio/internal/supplier        语句  96.77%  块  95.45%  (30/31 ; 21/22)

【整体覆盖率】 语句 95.16%  块 96.00%  (1692/1778 ; 1008/1050)
  核心包 creative-studio/internal/domain    语句  89.71%  块  88.52%
  核心包 creative-studio/internal/store     语句  99.72%  块  99.61%
  核心包 creative-studio/internal/service   语句  98.02%  块  97.25%
  核心包 creative-studio/internal/httpapi   语句  98.98%  块  98.45%
  核心包 creative-studio/internal/supplier  语句  96.77%  块  95.45%

【结论】PASS（整体与各核心包均 ≥ 85.0%）
```

## 四、失败项与建议
- 无失败项。
