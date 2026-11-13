package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"creative-studio/internal/domain"
	"creative-studio/internal/store"
)

// CallbackPayload 是供应商回调的载荷。
type CallbackPayload struct {
	// SupplierTaskID 是供应商任务号，用于关联平台侧的生成任务。
	SupplierTaskID string
	// Status 取值为 succeeded 或 failed。
	Status string
	// ImageURL 在成功时必须提供。
	ImageURL string
	// Reason 在失败时说明原因。
	Reason string
}

// CallbackResult 是回调处理结果。
type CallbackResult struct {
	// TaskID 是平台侧的生成任务 ID。
	TaskID string
	// Applied 表示回调是否真的改写了任务与需求状态。
	Applied bool
	// Ignored 为 true 表示回调被忽略（重复、冲突或迟到），原因写进 Message。
	Ignored bool
	// Message 是可读的处理结论。
	Message string
}

// HandleCallback 处理供应商回调（题面 R4）。
// 重复回调不重复创建候选；已终结的生成任务不被迟到或冲突回调改写；异常一律留痕可查。
func (s *Service) HandleCallback(ctx context.Context, cb CallbackPayload) (*CallbackResult, error) {
	if cb.SupplierTaskID == "" {
		return nil, fmt.Errorf("%w: 缺少供应商任务号", domain.ErrInvalidInput)
	}
	if cb.Status != "succeeded" && cb.Status != "failed" {
		return nil, fmt.Errorf("%w: 回调状态只能是 succeeded 或 failed", domain.ErrInvalidInput)
	}
	if cb.Status == "succeeded" && cb.ImageURL == "" {
		return nil, fmt.Errorf("%w: 成功回调必须提供图片地址", domain.ErrInvalidInput)
	}
	now := s.now()
	result := &CallbackResult{}
	err := s.store.Tx(ctx, func(tx store.Execer) error {
		task, err := s.store.GetTaskBySupplierTaskIDTx(ctx, tx, cb.SupplierTaskID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				// 关联不到已知任务：记异常事件，可查询。
				if err := s.store.InsertEventTx(ctx, tx, newEvent(nil, "", "", domain.EventCallbackUnknown,
					fmt.Sprintf("回调关联不到已知任务，供应商任务号=%s", cb.SupplierTaskID), now)); err != nil {
					return err
				}
				result.Ignored = true
				result.Message = "回调关联不到已知任务，已记入异常记录"
				return nil
			}
			return err
		}
		result.TaskID = task.ID
		// 题面 R4：已终结的任务不再被迟到或冲突回调改写。
		if task.Status.Terminal() {
			kind := domain.EventCallbackConflict
			msg := fmt.Sprintf("任务已处于终态 %s，回调未改写任务", task.Status)
			if task.Status == domain.TaskSucceeded && cb.Status == "succeeded" {
				kind = domain.EventCallbackDuplicate
				msg = "重复的成功回调，未重复创建候选"
			}
			if err := s.store.InsertEventTx(ctx, tx, newEvent(nil, task.RequirementID, task.ID, kind, msg, now)); err != nil {
				return err
			}
			result.Ignored = true
			result.Message = msg
			return nil
		}
		req, err := s.store.GetRequirementTx(ctx, tx, task.RequirementID)
		if err != nil {
			return wrapNotFound(err)
		}
		if cb.Status == "failed" {
			task.Status = domain.TaskFailed
			task.FailureReason = cb.Reason
			task.FinishedAt = now
			if err := s.store.UpdateTaskTx(ctx, tx, task); err != nil {
				return err
			}
			// 题面 R7：需求已撤销时，迟到的失败结果同样只保留审计，
			// 不得把需求改回「失败」，否则撤销的需求会被意外复活。
			if req.Status == domain.ReqWithdrawn {
				msg := "需求已撤销，迟到的失败结果仅保留审计，未改写需求状态"
				if err := s.store.InsertEventTx(ctx, tx, newEvent(nil, req.ID, task.ID, domain.EventLateResultIgnored, msg, now)); err != nil {
					return err
				}
				result.Applied = true
				result.Ignored = true
				result.Message = msg
				return nil
			}
			status := domain.NextStatusAfterAttemptFailure(task.AttemptNo)
			if err := s.store.UpdateRequirementStatusTx(ctx, tx, req.ID, status, task.ID, "", req.AttemptCount, now); err != nil {
				return err
			}
			if err := s.store.InsertEventTx(ctx, tx, newEvent(nil, req.ID, task.ID, domain.EventGenerationFailed,
				fmt.Sprintf("供应商报告生成失败：%s", cb.Reason), now)); err != nil {
				return err
			}
			result.Applied = true
			result.Message = "已记录生成失败"
			return nil
		}
		// 成功回调。
		task.Status = domain.TaskSucceeded
		task.ImageURL = cb.ImageURL
		task.FinishedAt = now
		if err := s.store.UpdateTaskTx(ctx, tx, task); err != nil {
			return err
		}
		// 题面 R7：需求已撤销时，迟到结果只保留审计，不恢复需求也不形成交付。
		if req.Status == domain.ReqWithdrawn {
			msg := "需求已撤销，迟到的成功结果仅保留审计，未恢复需求、未形成交付"
			if err := s.store.InsertEventTx(ctx, tx, newEvent(nil, req.ID, task.ID, domain.EventLateResultIgnored, msg, now)); err != nil {
				return err
			}
			result.Applied = true
			result.Ignored = true
			result.Message = msg
			return nil
		}
		// 题面 R6：发起下一次生成后，上一版候选只作历史。
		if err := s.store.ClearCurrentCandidatesTx(ctx, tx, req.ID); err != nil {
			return err
		}
		cand := &domain.Candidate{
			ID:            s.newID("cand"),
			TaskID:        task.ID,
			RequirementID: req.ID,
			AttemptNo:     task.AttemptNo,
			ImageURL:      cb.ImageURL,
			ReviewStatus:  domain.ReviewPending,
			IsCurrent:     true,
			CreatedAt:     now,
		}
		if err := s.store.InsertCandidateTx(ctx, tx, cand); err != nil {
			return err
		}
		if err := s.store.UpdateRequirementStatusTx(ctx, tx, req.ID, domain.ReqCandidatePending, task.ID, cand.ID, req.AttemptCount, now); err != nil {
			return err
		}
		if err := s.store.InsertEventTx(ctx, tx, newEvent(nil, req.ID, task.ID, domain.EventCandidateCreated,
			fmt.Sprintf("第 %d 次生成成功，生成候选待设计复核", task.AttemptNo), now)); err != nil {
			return err
		}
		if err := s.store.InsertEventTx(ctx, tx, newEvent(nil, req.ID, task.ID, domain.EventCallbackAccepted,
			"供应商成功回调已受理", now)); err != nil {
			return err
		}
		result.Applied = true
		result.Message = "已生成候选，等待设计复核"
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// SweepTimeouts 把超过时限仍未收到有效终态的任务判定为超时（题面 R3）。
// 返回被判定为超时的任务 ID 列表。超时不会自动重试，只把需求置为可再次发起的状态。
func (s *Service) SweepTimeouts(ctx context.Context) ([]string, error) {
	now := s.now()
	timeout := domain.EffectiveTimeout(s.cfg.ClockScale)
	cutoff := now.Add(-timeout)
	tasks, err := s.store.ListProcessingTasksBefore(ctx, cutoff)
	if err != nil {
		return nil, err
	}
	timedOut := []string{}
	for _, task := range tasks {
		ok := false
		err := s.store.Tx(ctx, func(tx store.Execer) error {
			current, err := s.store.GetTaskTx(ctx, tx, task.ID)
			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					return nil
				}
				return err
			}
			if current.Status != domain.TaskProcessing {
				return nil
			}
			current.Status = domain.TaskTimeout
			current.FailureReason = fmt.Sprintf("超过 %s 未收到有效终态，判定为超时", timeout)
			current.FinishedAt = now
			if err := s.store.UpdateTaskTx(ctx, tx, current); err != nil {
				return err
			}
			req, err := s.store.GetRequirementTx(ctx, tx, current.RequirementID)
			if err != nil {
				return err
			}
			status := domain.NextStatusAfterAttemptFailure(current.AttemptNo)
			if err := s.store.UpdateRequirementStatusTx(ctx, tx, req.ID, status, current.ID, "", req.AttemptCount, now); err != nil {
				return err
			}
			if err := s.store.InsertEventTx(ctx, tx, newEvent(nil, req.ID, current.ID, domain.EventGenerationTimeout,
				current.FailureReason, now)); err != nil {
				return err
			}
			ok = true
			return nil
		})
		if err != nil {
			return timedOut, err
		}
		if ok {
			timedOut = append(timedOut, task.ID)
		}
	}
	return timedOut, nil
}

// StartTimeoutSweeper 启动后台超时巡检协程，interval 为巡检间隔。
func (s *Service) StartTimeoutSweeper(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = s.SweepTimeouts(ctx)
			}
		}
	}()
}
