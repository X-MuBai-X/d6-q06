package service

import (
	"context"
	"testing"

	"creative-studio/internal/domain"
)

// TestAnomalyKindsRegistry 覆盖异常种类注册表：注册表内每个种类都应被识别为异常。
func TestAnomalyKindsRegistry(t *testing.T) {
	if len(AnomalyKinds) == 0 {
		t.Fatal("异常种类注册表不应为空")
	}
	seen := map[string]bool{}
	for _, kind := range AnomalyKinds {
		if kind == "" {
			t.Fatal("异常种类注册表不应包含空字符串")
		}
		if seen[kind] {
			t.Fatalf("异常种类注册表不应重复：%s", kind)
		}
		seen[kind] = true
		if !IsAnomalyKind(kind) {
			t.Fatalf("注册表内的异常种类应被识别为异常：%s", kind)
		}
	}
	// 注册表必须覆盖题面 R4 要求可查的全部异常种类。
	want := []string{
		domain.EventCallbackDuplicate, domain.EventCallbackConflict, domain.EventLateResultIgnored,
		domain.EventSignatureInvalid, domain.EventCallbackUnknown, domain.EventGenerationRejected,
		domain.EventGenerationFailed, domain.EventGenerationTimeout,
	}
	for _, kind := range want {
		if !IsAnomalyKind(kind) {
			t.Fatalf("异常种类 %s 未被注册", kind)
		}
	}
}

// TestIsAnomalyKindRejectsUnregistered 覆盖未注册的自定义种类不属于异常。
func TestIsAnomalyKindRejectsUnregistered(t *testing.T) {
	cases := []string{"", "custom_kind", "requirement_created", "callback_accepted"}
	for _, kind := range cases {
		if IsAnomalyKind(kind) {
			t.Fatalf("未注册的种类不应被识别为异常：%q", kind)
		}
	}
}

// TestListAnomaliesUsesRegistry 覆盖运营异常查询：只返回注册表内的异常事件。
func TestListAnomaliesUsesRegistry(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	if err := env.svc.RecordAnomaly(ctx, domain.EventSignatureInvalid, "", "", "回调签名校验失败"); err != nil {
		t.Fatalf("记录异常失败: %v", err)
	}
	anomalies, err := env.svc.ListAnomalies(ctx, env.operator, 10)
	if err != nil {
		t.Fatalf("查询异常失败: %v", err)
	}
	found := false
	for _, e := range anomalies {
		if !IsAnomalyKind(e.Kind) {
			t.Fatalf("异常列表不应包含非异常事件：%s", e.Kind)
		}
		if e.Kind == domain.EventSignatureInvalid {
			found = true
		}
	}
	if !found {
		t.Fatalf("签名无效异常应可被运营查到: %+v", anomalies)
	}
	// 非异常事件不应出现在异常列表中。
	if err := env.store.InsertEvent(ctx, &domain.Event{
		RequirementID: "req-anomaly", Kind: domain.EventCandidateCreated,
		Detail: "普通事件", CreatedAt: env.svc.now(),
	}); err != nil {
		t.Fatalf("写入普通事件失败: %v", err)
	}
	anomalies, err = env.svc.ListAnomalies(ctx, env.operator, 10)
	if err != nil {
		t.Fatalf("查询异常失败: %v", err)
	}
	for _, e := range anomalies {
		if e.Kind == domain.EventCandidateCreated {
			t.Fatalf("普通事件不应出现在异常列表中: %+v", anomalies)
		}
	}
}
