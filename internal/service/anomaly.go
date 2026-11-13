package service

import (
	"context"
	"fmt"

	"creative-studio/internal/domain"
)

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
