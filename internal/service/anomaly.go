package service

import (
	"context"
	"fmt"

	"creative-studio/internal/domain"
)

// AnomalyKinds 是运营异常查询覆盖的事件种类注册表（题面 R4：异常须可查）。
// 事件类型常量集中定义在 internal/domain/types.go（领域模型层），
// 而「哪些种类属于运营异常」属于业务策略，故注册表放在业务层 internal/service。
// 该注册表是异常种类的唯一事实来源：存储层按调用方传入的清单过滤，不再自带重复字面量。
var AnomalyKinds = []string{
	domain.EventCallbackDuplicate,
	domain.EventCallbackConflict,
	domain.EventLateResultIgnored,
	domain.EventSignatureInvalid,
	domain.EventCallbackUnknown,
	domain.EventGenerationRejected,
	domain.EventGenerationFailed,
	domain.EventGenerationTimeout,
}

// IsAnomalyKind 判断某个事件种类是否属于运营异常注册表。
func IsAnomalyKind(kind string) bool {
	for _, k := range AnomalyKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// RecordAnomaly 记录一条异常审计事件（供应商回调鉴权失败、回调关联不到任务等）。
func (s *Service) RecordAnomaly(ctx context.Context, kind, requirementID, taskID, detail string) error {
	evt := newEvent(nil, requirementID, taskID, kind, detail, s.now())
	return s.store.InsertEvent(ctx, evt)
}

// DescribeStatus 返回需求状态的可读说明。
func DescribeStatus(status domain.RequirementStatus) string {
	if label, ok := domain.StatusLabels()[status]; ok {
		return label
	}
	return fmt.Sprintf("%s", status)
}
