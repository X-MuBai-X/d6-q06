package domain

import (
	"errors"
	"testing"
	"time"
)

func TestRoleValid(t *testing.T) {
	cases := map[Role]bool{
		RoleRequester: true,
		RoleReviewer:  true,
		RoleOperator:  true,
		Role("admin"): false,
		Role(""):      false,
	}
	for role, want := range cases {
		if got := role.Valid(); got != want {
			t.Fatalf("Role(%q).Valid() = %v，期望 %v", role, got, want)
		}
	}
}

func TestTaskStatusTerminal(t *testing.T) {
	terminal := []TaskStatus{TaskSucceeded, TaskFailed, TaskRejected, TaskTimeout}
	for _, s := range terminal {
		if !s.Terminal() {
			t.Fatalf("%q 应为终态", s)
		}
	}
	if TaskProcessing.Terminal() {
		t.Fatal("processing 不应是终态")
	}
}

func TestCanStartGeneration(t *testing.T) {
	allowed := []RequirementStatus{ReqDraft, ReqFailed, ReqRejected, ReqApproved}
	for _, s := range allowed {
		if !CanStartGeneration(s) {
			t.Fatalf("%q 应允许发起生成", s)
		}
	}
	denied := []RequirementStatus{ReqGenerating, ReqCandidatePending, ReqDelivered, ReqExhausted, ReqWithdrawn}
	for _, s := range denied {
		if CanStartGeneration(s) {
			t.Fatalf("%q 不应允许发起生成", s)
		}
	}
}

func TestCanReviewAndAdopt(t *testing.T) {
	if !CanReview(ReqCandidatePending) {
		t.Fatal("待复核状态应允许复核")
	}
	if CanReview(ReqApproved) || CanReview(ReqDraft) {
		t.Fatal("非待复核状态不应允许复核")
	}
	if !CanAdopt(ReqApproved) {
		t.Fatal("复核通过应允许确认采用")
	}
	if CanAdopt(ReqCandidatePending) || CanAdopt(ReqDelivered) {
		t.Fatal("其它状态不应允许确认采用")
	}
}

func TestCanWithdraw(t *testing.T) {
	allowed := []RequirementStatus{ReqDraft, ReqGenerating, ReqCandidatePending, ReqRejected, ReqApproved, ReqFailed, ReqExhausted}
	for _, s := range allowed {
		if !CanWithdraw(s) {
			t.Fatalf("%q 应允许撤销", s)
		}
	}
	if CanWithdraw(ReqDelivered) {
		t.Fatal("已确认采用不应允许撤销")
	}
	if CanWithdraw(ReqWithdrawn) {
		t.Fatal("已撤销不应重复撤销")
	}
}

func TestNextStatusAfterAttemptFailure(t *testing.T) {
	if got := NextStatusAfterAttemptFailure(1); got != ReqFailed {
		t.Fatalf("第 1 次失败应为 failed，实际 %q", got)
	}
	if got := NextStatusAfterAttemptFailure(3); got != ReqExhausted {
		t.Fatalf("第 3 次失败应为 exhausted，实际 %q", got)
	}
	if got := NextStatusAfterAttemptFailure(2); got != ReqFailed {
		t.Fatalf("第 2 次失败应为 failed，实际 %q", got)
	}
}

func TestValidations(t *testing.T) {
	if err := ValidateActivityName("  "); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("空活动名称应报参数错误，实际 %v", err)
	}
	if err := ValidateActivityName(string(make([]rune, MaxActivityNameLen+1))); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("超长活动名称应报参数错误，实际 %v", err)
	}
	if err := ValidateActivityName("夏日活动"); err != nil {
		t.Fatalf("合法活动名称不应报错，实际 %v", err)
	}
	if err := ValidatePurpose(""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("空用途应报参数错误，实际 %v", err)
	}
	if err := ValidatePurpose(string(make([]rune, MaxPurposeLen+1))); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("超长用途应报参数错误，实际 %v", err)
	}
	if err := ValidateDescription("短"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("过短描述应报参数错误，实际 %v", err)
	}
	if err := ValidateDescription(string(make([]rune, MaxDescriptionLen+1))); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("超长描述应报参数错误，实际 %v", err)
	}
	if err := ValidateDescription("正常长度的图片描述"); err != nil {
		t.Fatalf("合法描述不应报错，实际 %v", err)
	}
	if err := ValidateRequestNo(""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("空请求编号应报参数错误，实际 %v", err)
	}
	if err := ValidateRequestNo(string(make([]rune, 65))); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("超长请求编号应报参数错误，实际 %v", err)
	}
	if err := ValidateReviewReason("   "); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("空复核原因应报参数错误，实际 %v", err)
	}
	if err := ValidateReviewReason(string(make([]rune, 501))); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("超长复核原因应报参数错误，实际 %v", err)
	}
	if err := ValidateReworkReason(""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("空重做原因应报参数错误，实际 %v", err)
	}
	if err := ValidateOperatorNote(""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("空跟进意见应报参数错误，实际 %v", err)
	}
	if err := ValidateOperatorNote(string(make([]rune, 1001))); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("超长跟进意见应报参数错误，实际 %v", err)
	}
}

func TestEffectiveTimeout(t *testing.T) {
	if got := EffectiveTimeout(1); got != 60*time.Second {
		t.Fatalf("倍率 1 的超时上限应为 60s，实际 %v", got)
	}
	if got := EffectiveTimeout(0.1); got != 6*time.Second {
		t.Fatalf("倍率 0.1 的超时上限应为 6s，实际 %v", got)
	}
	if got := EffectiveTimeout(0); got != 60*time.Second {
		t.Fatalf("非正倍率应回落到 60s，实际 %v", got)
	}
	if got := EffectiveTimeout(-1); got != 60*time.Second {
		t.Fatalf("负倍率应回落到 60s，实际 %v", got)
	}
}

func TestIsAnomalyEvent(t *testing.T) {
	anomalies := []string{EventCallbackDuplicate, EventCallbackConflict, EventLateResultIgnored,
		EventSignatureInvalid, EventCallbackUnknown, EventGenerationRejected, EventGenerationFailed, EventGenerationTimeout}
	for _, kind := range anomalies {
		if !IsAnomalyEvent(kind) {
			t.Fatalf("%q 应属于异常事件", kind)
		}
	}
	if IsAnomalyEvent(EventRequirementCreated) {
		t.Fatal("创建需求不应属于异常事件")
	}
}

func TestStatusLabels(t *testing.T) {
	labels := StatusLabels()
	if len(labels) != 9 {
		t.Fatalf("状态标签数量应为 9，实际 %d", len(labels))
	}
	if labels[ReqDelivered] == "" {
		t.Fatal("已确认采用应有中文标签")
	}
}

func TestFixedClock(t *testing.T) {
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	c := FixedClock{T: fixed}
	if !c.Now().Equal(fixed) {
		t.Fatalf("固定时钟应返回 %v，实际 %v", fixed, c.Now())
	}
	if (SystemClock{}).Now().IsZero() {
		t.Fatal("系统时钟不应为零值")
	}
}
