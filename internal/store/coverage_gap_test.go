package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"creative-studio/internal/domain"
)

// 本文件补齐 internal/store 中尚未被覆盖的代码块，重点是持久化层的失败分支：
// 数据库不可用、表结构不兼容、事务开启/提交失败、以及库中出现类型不符的脏数据。

// gapDriverState 控制 gap 驱动在执行语句时是否失败。
type gapDriverState struct {
	mu   sync.Mutex
	fail map[string]string
}

// setFail 设置「语句前缀 -> 错误信息」的失败规则；传 nil 表示全部放行。
func (s *gapDriverState) setFail(rules map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = rules
}

// match 判断某条语句是否命中失败规则。
func (s *gapDriverState) match(query string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for prefix, msg := range s.fail {
		if strings.HasPrefix(strings.ToUpper(query), strings.ToUpper(prefix)) {
			return msg, true
		}
	}
	return "", false
}

var gapState = &gapDriverState{}

// gapDriver 是可控失败的数据库驱动，仅用于覆盖事务开启与提交失败分支。
type gapDriver struct{}

func (gapDriver) Open(string) (driver.Conn, error) { return &gapConn{}, nil }

// gapConn 实现 driver.Conn 与 driver.ExecerContext。
type gapConn struct{}

func (c *gapConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("gap 驱动不支持预编译语句")
}

func (c *gapConn) Close() error { return nil }

func (c *gapConn) Begin() (driver.Tx, error) {
	return nil, errors.New("gap 驱动不支持事务")
}

func (c *gapConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if msg, ok := gapState.match(query); ok {
		return nil, errors.New(msg)
	}
	return driver.RowsAffected(0), nil
}

func (c *gapConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return nil, errors.New("gap 驱动不支持查询")
}

func init() {
	sql.Register("creative-studio-gap-driver", gapDriver{})
}

// TestGapStoreTransactionBeginAndCommitFailures 覆盖事务开启失败与提交失败。
// 业务含义：事务开启失败时业务回调不得被执行；提交失败时必须把错误上抛，
// 让上层知道本次写入没有落库。
func TestGapStoreTransactionBeginAndCommitFailures(t *testing.T) {
	t.Cleanup(func() { gapState.setFail(nil) })
	db, err := sql.Open("creative-studio-gap-driver", "gap")
	if err != nil {
		t.Fatalf("打开 gap 数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := &Store{db: db}
	ctx := context.Background()

	// 开启事务失败：回调不应被执行。
	gapState.setFail(map[string]string{"BEGIN IMMEDIATE": "注入的事务开启故障"})
	called := false
	err = st.Tx(ctx, func(Execer) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("开启事务失败时必须返回错误")
	}
	if !strings.Contains(err.Error(), "开启事务失败") {
		t.Fatalf("错误信息应说明是开启事务失败，实际 %v", err)
	}
	if called {
		t.Fatal("开启事务失败时业务回调不应被执行")
	}

	// 提交失败：必须上抛错误，说明本次写入没有落库。
	gapState.setFail(map[string]string{"COMMIT": "注入的事务提交故障"})
	called = false
	err = st.Tx(ctx, func(Execer) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("提交事务失败时必须返回错误")
	}
	if !strings.Contains(err.Error(), "提交事务失败") {
		t.Fatalf("错误信息应说明是提交事务失败，实际 %v", err)
	}
	if !called {
		t.Fatal("提交失败时业务回调应当已执行")
	}
}

// TestGapStoreSchemaMigrationFailure 覆盖既有库结构与当前结构不兼容时的失败。
// 业务含义：老库的 requirements 表缺少 owner_id 列时，建索引必须失败并明确报错，
// 不能带着半初始化状态继续提供服务。
func TestGapStoreSchemaMigrationFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("打开旧库失败: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE requirements (id TEXT PRIMARY KEY)"); err != nil {
		t.Fatalf("构造旧库失败: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("关闭旧库失败: %v", err)
	}

	st, err := Open(path)
	if err == nil {
		_ = st.Close()
		t.Fatal("结构不兼容时打开数据库必须报错")
	}
	if !strings.Contains(err.Error(), "初始化表结构失败") {
		t.Fatalf("错误信息应说明是表结构初始化失败，实际 %v", err)
	}
}

// TestGapStoreClosedDatabaseErrorBranches 覆盖数据库不可用时的全部读写分支。
// 业务含义：底层连接不可用时，每个接口都必须返回带上下文的错误，
// 既不能 panic，也不能静默返回空数据让上层误判为「没有数据」。
func TestGapStoreClosedDatabaseErrorBranches(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	seedRequirement(t, st, "req-1")
	seedTask(t, st, "task-1", "req-1", "req-no-1", 1, domain.TaskProcessing)

	if err := st.Close(); err != nil {
		t.Fatalf("关闭数据库失败: %v", err)
	}

	check := func(name string, err error, want string) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s：数据库不可用时必须返回错误", name)
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("%s：错误信息应包含 %q，实际 %v", name, want, err)
		}
	}

	// 需求相关
	_, err := st.GetRequirement(ctx, "req-1")
	check("读取需求", err, "读取需求失败")
	_, err = st.ListRequirements(ctx, RequirementFilter{})
	check("查询需求列表", err, "查询需求列表失败")
	err = st.UpdateDescription(ctx, "req-1", "新描述", time.Now())
	check("更新描述", err, "更新描述失败")
	err = st.UpdateRequirementStatus(ctx, "req-1", domain.ReqGenerating, "task-1", "", 1, time.Now())
	check("更新需求状态", err, "更新需求状态失败")
	err = st.InsertRequirement(ctx, &domain.Requirement{
		ID: "req-2", OwnerID: "o", ActivityName: "a", Purpose: "p", Description: "d",
		Status: domain.ReqDraft, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	check("写入需求", err, "写入需求失败")

	// 任务相关
	_, err = st.GetTask(ctx, "task-1")
	check("读取生成任务", err, "读取生成任务失败")
	_, err = st.GetTaskByRequestNo(ctx, "req-no-1")
	check("按请求编号读取任务", err, "读取生成任务失败")
	_, err = st.GetTaskByRequirementRequestNo(ctx, "req-1", "req-no-1")
	check("按需求与请求编号读取任务", err, "读取生成任务失败")
	_, err = st.GetTaskBySupplierTaskID(ctx, "sup-1")
	check("按供应商任务号读取任务", err, "读取生成任务失败")
	_, err = st.FindProcessingTask(ctx, "req-1")
	check("查找处理中任务", err, "读取生成任务失败")
	_, err = st.ListTasks(ctx, "req-1")
	check("查询任务列表", err, "查询生成任务失败")
	_, err = st.ListProcessingTasksBefore(ctx, time.Now())
	check("查询超时任务", err, "查询超时任务失败")
	err = st.UpdateTask(ctx, &domain.GenerationTask{ID: "task-1", Status: domain.TaskSucceeded})
	check("更新生成任务", err, "更新生成任务失败")
	err = st.InsertTask(ctx, &domain.GenerationTask{
		ID: "task-2", RequirementID: "req-1", RequestNo: "req-no-2", AttemptNo: 2,
		DescriptionSnapshot: "x", Status: domain.TaskFailed, CreatedAt: time.Now(),
	})
	check("写入生成任务", err, "写入生成任务失败")

	// 候选相关
	_, err = st.GetCandidate(ctx, "cand-1")
	check("读取候选", err, "读取候选失败")
	_, err = st.GetCurrentCandidate(ctx, "req-1")
	check("读取当前候选", err, "读取当前候选失败")
	_, err = st.ListCandidates(ctx, "req-1")
	check("查询候选列表", err, "查询候选列表失败")
	err = st.InsertCandidate(ctx, &domain.Candidate{
		ID: "cand-1", TaskID: "task-1", RequirementID: "req-1", AttemptNo: 1,
		ImageURL: "/static/img/a.svg", ReviewStatus: domain.ReviewPending, CreatedAt: time.Now(),
	})
	check("写入候选", err, "写入候选失败")
	err = st.UpdateCandidateReview(ctx, &domain.Candidate{ID: "cand-1", ReviewStatus: domain.ReviewApproved})
	check("更新复核结论", err, "更新复核结论失败")
	err = st.ClearCurrentCandidates(ctx, "req-1")
	check("清除当前候选标记", err, "更新候选历史标记失败")

	// 审计事件相关
	err = st.InsertEvent(ctx, &domain.Event{RequirementID: "req-1", Kind: domain.EventRequirementCreated, CreatedAt: time.Now()})
	check("写入审计事件", err, "写入审计事件失败")
	_, err = st.ListEvents(ctx, "req-1")
	check("查询审计事件", err, "查询审计事件失败")
	_, err = st.ListAnomalyEvents(ctx, []string{domain.EventCallbackDuplicate}, 5)
	check("查询异常事件", err, "查询异常事件失败")
	_, err = st.ListRecentEvents(ctx, 5)
	check("查询最近事件", err, "查询最近事件失败")

	// 账号与会话相关
	_, err = st.GetUser(ctx, "demand-a")
	check("读取账号", err, "读取账号失败")
	err = st.InsertUser(ctx, &domain.User{ID: "demand-a", Name: "n", Role: domain.RoleRequester, PasswordHash: "h", Salt: "s"})
	check("写入账号", err, "写入账号失败")
	err = st.InsertSession(ctx, "tok", "demand-a", time.Now())
	check("写入会话", err, "写入会话失败")
	_, err = st.GetSessionUser(ctx, "tok")
	check("读取会话账号", err, "读取会话失败")
	err = st.DeleteSession(ctx, "tok")
	check("注销会话", err, "注销会话失败")

	// 事务上下文内的读写（直接以已关闭的连接作为 Execer）
	db := st.DB()
	_, err = st.GetRequirementTx(ctx, db, "req-1")
	check("事务内读取需求", err, "读取需求失败")
	_, err = st.GetTaskTx(ctx, db, "task-1")
	check("事务内读取任务", err, "读取生成任务失败")
	_, err = st.GetTaskByRequestNoTx(ctx, db, "req-no-1")
	check("事务内按请求编号读取任务", err, "读取生成任务失败")
	_, err = st.GetTaskByRequirementRequestNoTx(ctx, db, "req-1", "req-no-1")
	check("事务内按需求与请求编号读取任务", err, "读取生成任务失败")
	_, err = st.GetTaskBySupplierTaskIDTx(ctx, db, "sup-1")
	check("事务内按供应商任务号读取任务", err, "读取生成任务失败")
	_, err = st.FindProcessingTaskTx(ctx, db, "req-1")
	check("事务内查找处理中任务", err, "读取生成任务失败")
	_, err = st.GetCandidateTx(ctx, db, "cand-1")
	check("事务内读取候选", err, "读取候选失败")
	_, err = st.GetCurrentCandidateTx(ctx, db, "req-1")
	check("事务内读取当前候选", err, "读取当前候选失败")
	err = st.InsertTaskTx(ctx, db, &domain.GenerationTask{
		ID: "task-3", RequirementID: "req-1", RequestNo: "req-no-3", AttemptNo: 3,
		DescriptionSnapshot: "x", Status: domain.TaskFailed, CreatedAt: time.Now(),
	})
	check("事务内写入任务", err, "写入生成任务失败")
	err = st.UpdateTaskTx(ctx, db, &domain.GenerationTask{ID: "task-1", Status: domain.TaskSucceeded})
	check("事务内更新任务", err, "更新生成任务失败")
	err = st.UpdateRequirementStatusTx(ctx, db, "req-1", domain.ReqGenerating, "task-1", "", 1, time.Now())
	check("事务内更新需求状态", err, "更新需求状态失败")
	err = st.UpdateRequirementDescriptionTx(ctx, db, "req-1", "新描述", time.Now())
	check("事务内更新描述", err, "更新描述失败")
	err = st.InsertCandidateTx(ctx, db, &domain.Candidate{
		ID: "cand-2", TaskID: "task-1", RequirementID: "req-1", AttemptNo: 1,
		ImageURL: "/static/img/b.svg", ReviewStatus: domain.ReviewPending, CreatedAt: time.Now(),
	})
	check("事务内写入候选", err, "写入候选失败")
	err = st.UpdateCandidateReviewTx(ctx, db, &domain.Candidate{ID: "cand-1", ReviewStatus: domain.ReviewApproved})
	check("事务内更新复核结论", err, "更新复核结论失败")
	err = st.ClearCurrentCandidatesTx(ctx, db, "req-1")
	check("事务内清除当前候选标记", err, "更新候选历史标记失败")
	err = st.InsertEventTx(ctx, db, &domain.Event{RequirementID: "req-1", Kind: domain.EventRequirementCreated, CreatedAt: time.Now()})
	check("事务内写入审计事件", err, "写入审计事件失败")

	// 事务本身：连接不可用时必须报错，且业务回调不应执行。
	called := false
	err = st.Tx(ctx, func(Execer) error {
		called = true
		return nil
	})
	check("开启事务", err, "获取数据库连接失败")
	if called {
		t.Fatal("数据库不可用时事务回调不应被执行")
	}
}

// TestGapStoreDirtyRowIsReportedAsError 覆盖库中出现类型不符的脏数据时的读失败。
// 业务含义：整数列被写成二进制数据时，读接口必须明确报错，
// 不能把脏值当成 0 静默写进业务判断。
func TestGapStoreDirtyRowIsReportedAsError(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	t.Run("需求列表", func(t *testing.T) {
		st := newTestStore(t)
		_, err := st.DB().Exec(`INSERT INTO requirements
			(id, owner_id, activity_name, purpose, description, status, current_task_id, current_candidate, attempt_count, created_at, updated_at)
			VALUES ('req-dirty','demand-a','活动','用途','描述文本','draft','','', x'00ff', ?, ?)`, now, now)
		if err != nil {
			t.Fatalf("写入脏数据失败: %v", err)
		}
		if _, err := st.ListRequirements(ctx, RequirementFilter{}); err == nil {
			t.Fatal("需求列表遇到脏数据必须报错")
		} else if !strings.Contains(err.Error(), "读取需求列表失败") {
			t.Fatalf("错误信息应说明是读取需求列表失败，实际 %v", err)
		}
	})

	t.Run("任务列表", func(t *testing.T) {
		st := newTestStore(t)
		seedRequirement(t, st, "req-1")
		_, err := st.DB().Exec(`INSERT INTO generation_tasks
			(id, requirement_id, request_no, attempt_no, description_snapshot, status, supplier_task_id, image_url, failure_reason, created_at, finished_at)
			VALUES ('task-dirty','req-1','req-no-dirty', x'00ff','快照','processing','','','', ?, '')`, now)
		if err != nil {
			t.Fatalf("写入脏数据失败: %v", err)
		}
		if _, err := st.ListTasks(ctx, "req-1"); err == nil {
			t.Fatal("任务列表遇到脏数据必须报错")
		} else if !strings.Contains(err.Error(), "读取生成任务失败") {
			t.Fatalf("错误信息应说明是读取生成任务失败，实际 %v", err)
		}
		if _, err := st.ListProcessingTasksBefore(ctx, time.Now()); err == nil {
			t.Fatal("超时任务筛选遇到脏数据必须报错")
		} else if !strings.Contains(err.Error(), "读取超时任务失败") {
			t.Fatalf("错误信息应说明是读取超时任务失败，实际 %v", err)
		}
	})

	t.Run("候选列表", func(t *testing.T) {
		st := newTestStore(t)
		seedRequirement(t, st, "req-1")
		_, err := st.DB().Exec(`INSERT INTO candidates
			(id, task_id, requirement_id, attempt_no, image_url, review_status, review_reason, reviewer_id, reviewed_at, is_current, created_at)
			VALUES ('cand-dirty','task-1','req-1', x'00ff','/static/img/a.svg','pending','','','',1, ?)`, now)
		if err != nil {
			t.Fatalf("写入脏数据失败: %v", err)
		}
		if _, err := st.ListCandidates(ctx, "req-1"); err == nil {
			t.Fatal("候选列表遇到脏数据必须报错")
		} else if !strings.Contains(err.Error(), "读取候选列表失败") {
			t.Fatalf("错误信息应说明是读取候选列表失败，实际 %v", err)
		}
	})
}

// TestGapStoreHistoricalCandidateIsNotCurrent 覆盖历史候选的当前标记落库。
// 业务含义：题面 R6 要求上一版候选只作历史，落库时 is_current 必须为 0，
// 「当前候选」查询不应再命中它。
func TestGapStoreHistoricalCandidateIsNotCurrent(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	seedRequirement(t, st, "req-1")
	seedTask(t, st, "task-1", "req-1", "req-no-1", 1, domain.TaskSucceeded)

	historical := &domain.Candidate{
		ID: "cand-old", TaskID: "task-1", RequirementID: "req-1", AttemptNo: 1,
		ImageURL: "/static/img/old.svg", ReviewStatus: domain.ReviewPending,
		IsCurrent: false, CreatedAt: time.Now(),
	}
	if err := st.InsertCandidate(ctx, historical); err != nil {
		t.Fatalf("写入历史候选失败: %v", err)
	}
	got, err := st.GetCandidate(ctx, "cand-old")
	if err != nil {
		t.Fatalf("读取历史候选失败: %v", err)
	}
	if got.IsCurrent {
		t.Fatal("历史候选的当前标记应为 false")
	}
	if _, err := st.GetCurrentCandidate(ctx, "req-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("历史候选不应被当作当前候选，实际 %v", err)
	}
}

// TestGapStoreSessionAndUserNotFound 覆盖账号与会话缺失时的语义化错误。
// 业务含义：会话令牌无效必须返回 ErrUnauthorized（上层据此要求重新登录），
// 而不是 ErrNotFound。
func TestGapStoreSessionAndUserNotFound(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if _, err := st.GetSessionUser(ctx, "missing-token"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("无效令牌应返回 ErrUnauthorized，实际 %v", err)
	}
	if _, err := st.GetUser(ctx, "missing-user"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("不存在的账号应返回 ErrNotFound，实际 %v", err)
	}
}

// TestGapStoreEventTableWithBrokenIDColumn 覆盖审计事件表被破坏（id 列类型不再是整数）时的读失败。
// 业务含义：迁移出错导致事件主键类型异常时，审计读取接口必须明确报错，
// 不能把读不出来的审计流水当成「没有事件」。
func TestGapStoreEventTableWithBrokenIDColumn(t *testing.T) {
	ctx := context.Background()
	breakEvents := func(t *testing.T, st *Store) {
		t.Helper()
		stmts := []string{
			"ALTER TABLE events RENAME TO events_orig",
			`CREATE TABLE events (id TEXT, requirement_id TEXT NOT NULL DEFAULT '', task_id TEXT NOT NULL DEFAULT '',
				actor_id TEXT NOT NULL DEFAULT '', actor_role TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL,
				detail TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL)`,
			"INSERT INTO events SELECT CAST(id AS TEXT), requirement_id, task_id, actor_id, actor_role, kind, detail, created_at FROM events_orig",
			"DROP TABLE events_orig",
		}
		for _, stmt := range stmts {
			if _, err := st.DB().Exec(stmt); err != nil {
				t.Fatalf("破坏事件表失败（%s）: %v", stmt, err)
			}
		}
	}

	t.Run("事件列表", func(t *testing.T) {
		st := newTestStore(t)
		breakEvents(t, st)
		if _, err := st.DB().Exec(`INSERT INTO events
			(requirement_id, task_id, actor_id, actor_role, kind, detail, created_at)
			VALUES ('req-1','task-1','demand-a','requester','requirement_created','创建需求','2026-01-01T00:00:00Z')`); err != nil {
			t.Fatalf("写入事件失败: %v", err)
		}
		if _, err := st.ListEvents(ctx, "req-1"); err == nil {
			t.Fatal("事件列表遇到不可读的事件主键必须报错")
		} else if !strings.Contains(err.Error(), "读取审计事件失败") {
			t.Fatalf("错误信息应说明是读取审计事件失败，实际 %v", err)
		}
	})

	t.Run("异常列表与最近事件", func(t *testing.T) {
		st := newTestStore(t)
		breakEvents(t, st)
		if _, err := st.DB().Exec(`INSERT INTO events
			(requirement_id, task_id, actor_id, actor_role, kind, detail, created_at)
			VALUES ('req-1','task-1','demand-a','requester','callback_duplicate','重复回调','2026-01-01T00:00:00Z')`); err != nil {
			t.Fatalf("写入事件失败: %v", err)
		}
		if _, err := st.ListAnomalyEvents(ctx, []string{domain.EventCallbackDuplicate}, 5); err == nil {
			t.Fatal("异常列表遇到不可读的事件主键必须报错")
		} else if !strings.Contains(err.Error(), "读取异常事件失败") {
			t.Fatalf("错误信息应说明是读取异常事件失败，实际 %v", err)
		}
		if _, err := st.ListRecentEvents(ctx, 5); err == nil {
			t.Fatal("最近事件遇到不可读的事件主键必须报错")
		} else if !strings.Contains(err.Error(), "读取最近事件失败") {
			t.Fatalf("错误信息应说明是读取最近事件失败，实际 %v", err)
		}
	})
}
