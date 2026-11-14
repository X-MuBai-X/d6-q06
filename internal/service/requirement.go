package service

import (
	"context"
	"errors"
	"fmt"

	"creative-studio/internal/domain"
	"creative-studio/internal/store"
	"creative-studio/internal/supplier"
)

// CreateRequirementInput 是创建素材需求的入参。
type CreateRequirementInput struct {
	ActivityName string
	Purpose      string
	Description  string
}

// CreateRequirement 创建素材需求（题面 R1）。
func (s *Service) CreateRequirement(ctx context.Context, actor *domain.User, in CreateRequirementInput) (*domain.Requirement, error) {
	if actor.Role != domain.RoleRequester {
		return nil, fmt.Errorf("%w: 只有需求人可以创建需求", domain.ErrForbidden)
	}
	if err := domain.ValidateActivityName(in.ActivityName); err != nil {
		return nil, err
	}
	if err := domain.ValidatePurpose(in.Purpose); err != nil {
		return nil, err
	}
	if err := domain.ValidateDescription(in.Description); err != nil {
		return nil, err
	}
	now := s.now()
	req := &domain.Requirement{
		ID:           s.newID("req"),
		OwnerID:      actor.ID,
		ActivityName: in.ActivityName,
		Purpose:      in.Purpose,
		Description:  in.Description,
		Status:       domain.ReqDraft,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.store.InsertRequirement(ctx, req); err != nil {
		return nil, err
	}
	s.recordEvent(ctx, newEvent(actor, req.ID, "", domain.EventRequirementCreated,
		fmt.Sprintf("创建需求：活动=%s，用途=%s", req.ActivityName, req.Purpose), now))
	return req, nil
}

// UpdateDescription 修改需求当前的图片描述（题面 R1：历史生成任务的描述快照不受影响）。
func (s *Service) UpdateDescription(ctx context.Context, actor *domain.User, requirementID, description string) error {
	now := s.now()
	return s.store.Tx(ctx, func(tx store.Execer) error {
		req, err := s.store.GetRequirementTx(ctx, tx, requirementID)
		if err != nil {
			return wrapNotFound(err)
		}
		if !actorCanOperate(actor, req) {
			return fmt.Errorf("%w: 只有需求人可以修改自己的需求", domain.ErrForbidden)
		}
		if err := domain.ValidateDescription(description); err != nil {
			return err
		}
		if !domain.CanStartGeneration(req.Status) {
			return fmt.Errorf("%w: 当前状态 %s 不允许修改描述", domain.ErrInvalidState, req.Status)
		}
		if err := s.store.UpdateRequirementDescriptionTx(ctx, tx, requirementID, description, now); err != nil {
			return err
		}
		return s.store.InsertEventTx(ctx, tx, newEvent(actor, requirementID, "", domain.EventDescriptionUpdated,
			"修改图片描述，历史生成的描述快照保持不变", now))
	})
}

// StartGeneration 发起一次生成（题面 R1/R2/R3）。
// requestNo 是幂等键：同一请求编号重复发起不会创建第二个供应商任务。
func (s *Service) StartGeneration(ctx context.Context, actor *domain.User, requirementID, requestNo string) (*domain.GenerationTask, error) {
	if actor.Role != domain.RoleRequester {
		return nil, fmt.Errorf("%w: 只有需求人可以发起生成", domain.ErrForbidden)
	}
	if err := domain.ValidateRequestNo(requestNo); err != nil {
		return nil, err
	}
	now := s.now()
	var (
		task    *domain.GenerationTask
		created bool
	)
	err := s.store.Tx(ctx, func(tx store.Execer) error {
		req, err := s.store.GetRequirementTx(ctx, tx, requirementID)
		if err != nil {
			return wrapNotFound(err)
		}
		if !actorCanOperate(actor, req) {
			return fmt.Errorf("%w: 只有需求人可以对自己的需求发起生成", domain.ErrForbidden)
		}
		// 题面 R7 与 AGENTS §4 状态表：已撤销（以及已交付、次数用尽、待复核）的需求
		// 一律不得发起生成，已撤销的需求不得被再次发起而复活为 generating。
		// generating 状态放行到下面的「已有处理中任务」冲突判定，保持 R2 的既有口径。
		if req.Status != domain.ReqGenerating && !domain.CanStartGeneration(req.Status) {
			return fmt.Errorf("%w: 当前状态 %s 不允许发起生成", domain.ErrInvalidState, req.Status)
		}
		// 幂等：同一需求下同一请求编号命中已有任务时直接返回，不再创建供应商任务。
		existing, err := s.store.GetTaskByRequirementRequestNoTx(ctx, tx, requirementID, requestNo)
		if err == nil {
			task = existing
			created = false
			return nil
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		// 题面 R2：幂等键按需求隔离。同一请求编号若已属于其它需求，必须明确拒绝，
		// 不得返回别的需求的任务对象（更不得静默不执行却返回 202）。
		if other, err := s.store.GetTaskByRequestNoTx(ctx, tx, requestNo); err == nil {
			return fmt.Errorf("%w: 该请求编号已属于需求 %s，不能跨需求复用", domain.ErrConflict, other.RequirementID)
		} else if !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		// 题面 R2：同一需求最多有一个处理中生成任务。
		if _, err := s.store.FindProcessingTaskTx(ctx, tx, requirementID); err == nil {
			return fmt.Errorf("%w: 该需求已有处理中的生成任务", domain.ErrConflict)
		} else if !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		if req.AttemptCount >= domain.MaxAttempts {
			return fmt.Errorf("%w: 已发起 %d 次生成，达到上限", domain.ErrTooManyAttempts, req.AttemptCount)
		}
		attempt := req.AttemptCount + 1
		t := &domain.GenerationTask{
			ID:                  s.newID("task"),
			RequirementID:       requirementID,
			RequestNo:           requestNo,
			AttemptNo:           attempt,
			DescriptionSnapshot: req.Description,
			Status:              domain.TaskProcessing,
			CreatedAt:           now,
		}
		if err := s.store.InsertTaskTx(ctx, tx, t); err != nil {
			if isUniqueErr(err) {
				return fmt.Errorf("%w: 该请求编号已存在或已有处理中任务", domain.ErrConflict)
			}
			return err
		}
		if err := s.store.UpdateRequirementStatusTx(ctx, tx, requirementID, domain.ReqGenerating, t.ID, "", attempt, now); err != nil {
			return err
		}
		if err := s.store.InsertEventTx(ctx, tx, newEvent(actor, requirementID, t.ID, domain.EventGenerationStarted,
			fmt.Sprintf("发起第 %d 次生成，请求编号=%s，描述快照已保存", attempt, requestNo), now)); err != nil {
			return err
		}
		task = t
		created = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !created {
		// 幂等命中：不重复调用供应商。
		return task, nil
	}
	return s.submitToSupplier(ctx, actor, task)
}

// submitToSupplier 把生成任务提交给模拟供应商，并把明确答复写回任务与需求状态。
func (s *Service) submitToSupplier(ctx context.Context, actor *domain.User, task *domain.GenerationTask) (*domain.GenerationTask, error) {
	resp, err := s.supplier.Submit(ctx, supplierSubmit(task))
	if err != nil {
		// 本题不处理「提交结果未知」，模拟提交异常按「提交失败/拒绝」收口并留痕。
		reason := fmt.Sprintf("提交供应商失败：%v", err)
		s.finalizeRejected(ctx, actor, task, reason)
		return nil, fmt.Errorf("%w: %s", domain.ErrConflict, reason)
	}
	if !resp.Accepted {
		reason := resp.Reason
		if reason == "" {
			reason = "供应商拒绝受理本次生成"
		}
		s.finalizeRejected(ctx, actor, task, reason)
		return nil, fmt.Errorf("%w: 供应商拒绝受理：%s", domain.ErrConflict, reason)
	}
	now := s.now()
	task.SupplierTaskID = resp.TaskID
	task.Status = domain.TaskProcessing
	if err := s.store.UpdateTask(ctx, task); err != nil {
		return nil, err
	}
	s.recordEvent(ctx, newEvent(actor, task.RequirementID, task.ID, domain.EventCallbackAccepted,
		fmt.Sprintf("供应商已受理，供应商任务号=%s", resp.TaskID), now))
	return task, nil
}

// finalizeRejected 把被拒绝或提交失败的任务落到终态，并同步需求状态。
func (s *Service) finalizeRejected(ctx context.Context, actor *domain.User, task *domain.GenerationTask, reason string) {
	now := s.now()
	task.Status = domain.TaskRejected
	task.FailureReason = reason
	task.FinishedAt = now
	_ = s.store.Tx(ctx, func(tx store.Execer) error {
		if err := s.store.UpdateTaskTx(ctx, tx, task); err != nil {
			return err
		}
		req, err := s.store.GetRequirementTx(ctx, tx, task.RequirementID)
		if err != nil {
			return err
		}
		status := domain.NextStatusAfterAttemptFailure(task.AttemptNo)
		if err := s.store.UpdateRequirementStatusTx(ctx, tx, req.ID, status, task.ID, "", req.AttemptCount, now); err != nil {
			return err
		}
		return s.store.InsertEventTx(ctx, tx, newEvent(actor, req.ID, task.ID, domain.EventGenerationRejected,
			fmt.Sprintf("第 %d 次生成未受理：%s", task.AttemptNo, reason), now))
	})
}

// ReviewCandidate 设计复核人审核当前候选（题面 R5/R6）。
// candidateID 为空表示复核该需求的当前候选；给出时必须是当前候选，
// 历史候选（发起过一次新的生成之后）会以 ErrNotCurrentCandidate 明确拒绝。
func (s *Service) ReviewCandidate(ctx context.Context, actor *domain.User, requirementID, candidateID string, approved bool, reason string) (*domain.Candidate, error) {
	if actor.Role != domain.RoleReviewer {
		return nil, fmt.Errorf("%w: 只有设计复核人可以复核", domain.ErrForbidden)
	}
	if !approved {
		if err := domain.ValidateReviewReason(reason); err != nil {
			return nil, err
		}
	}
	now := s.now()
	var updated *domain.Candidate
	err := s.store.Tx(ctx, func(tx store.Execer) error {
		req, err := s.store.GetRequirementTx(ctx, tx, requirementID)
		if err != nil {
			return wrapNotFound(err)
		}
		var cand *domain.Candidate
		if candidateID != "" {
			cand, err = s.store.GetCandidateTx(ctx, tx, candidateID)
			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					return fmt.Errorf("%w: 候选不存在", domain.ErrNotFound)
				}
				return err
			}
			if cand.RequirementID != requirementID {
				return fmt.Errorf("%w: 候选不属于该需求", domain.ErrInvalidInput)
			}
			// 题面 R6：上一版候选只作历史，不能再被审核。
			if !cand.IsCurrent {
				return fmt.Errorf("%w: 该候选已是历史版本，不能复核", domain.ErrNotCurrentCandidate)
			}
		} else {
			cand, err = s.store.GetCurrentCandidateTx(ctx, tx, requirementID)
			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					return fmt.Errorf("%w: 该需求当前没有待复核的候选", domain.ErrNotFound)
				}
				return err
			}
			if !cand.IsCurrent {
				return fmt.Errorf("%w: 该候选已是历史版本，不能复核", domain.ErrNotCurrentCandidate)
			}
		}
		if !domain.CanReview(req.Status) {
			return fmt.Errorf("%w: 当前状态 %s 不允许复核", domain.ErrInvalidState, req.Status)
		}
		status := domain.ReviewRejected
		reqStatus := domain.ReqRejected
		detail := fmt.Sprintf("复核不通过，原因：%s", reason)
		if approved {
			status = domain.ReviewApproved
			reqStatus = domain.ReqApproved
			detail = "复核通过，等待需求人确认采用"
		}
		cand.ReviewStatus = status
		cand.ReviewReason = reason
		cand.ReviewerID = actor.ID
		cand.ReviewedAt = now
		if err := s.store.UpdateCandidateReviewTx(ctx, tx, cand); err != nil {
			return err
		}
		if err := s.store.UpdateRequirementStatusTx(ctx, tx, req.ID, reqStatus, req.CurrentTaskID, cand.ID, req.AttemptCount, now); err != nil {
			return err
		}
		if err := s.store.InsertEventTx(ctx, tx, newEvent(actor, req.ID, cand.TaskID, domain.EventCandidateReviewed,
			detail, now)); err != nil {
			return err
		}
		updated = cand
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// Adopt 需求人确认采用（题面 R5：确认采用才算交付完成；R6：历史候选不能被确认）。
func (s *Service) Adopt(ctx context.Context, actor *domain.User, requirementID, candidateID string) (*domain.Requirement, error) {
	now := s.now()
	var out *domain.Requirement
	err := s.store.Tx(ctx, func(tx store.Execer) error {
		req, err := s.store.GetRequirementTx(ctx, tx, requirementID)
		if err != nil {
			return wrapNotFound(err)
		}
		if !actorCanOperate(actor, req) {
			return fmt.Errorf("%w: 只有需求人可以确认采用", domain.ErrForbidden)
		}
		if candidateID != "" {
			cand, err := s.store.GetCandidateTx(ctx, tx, candidateID)
			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					return fmt.Errorf("%w: 候选不存在", domain.ErrNotFound)
				}
				return err
			}
			if cand.RequirementID != requirementID {
				return fmt.Errorf("%w: 候选不属于该需求", domain.ErrInvalidInput)
			}
			if !cand.IsCurrent {
				return fmt.Errorf("%w: 该候选已是历史版本，不能确认采用", domain.ErrNotCurrentCandidate)
			}
		}
		if !domain.CanAdopt(req.Status) {
			return fmt.Errorf("%w: 当前状态 %s 不允许确认采用", domain.ErrInvalidState, req.Status)
		}
		if err := s.store.UpdateRequirementStatusTx(ctx, tx, req.ID, domain.ReqDelivered, req.CurrentTaskID, req.CurrentCandidate, req.AttemptCount, now); err != nil {
			return err
		}
		if err := s.store.InsertEventTx(ctx, tx, newEvent(actor, req.ID, req.CurrentTaskID, domain.EventAdopted,
			"需求人确认采用，交付完成", now)); err != nil {
			return err
		}
		req.Status = domain.ReqDelivered
		req.UpdatedAt = now
		out = req
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RequestRework 需求人对已通过的结果提出重做原因（题面 R5）。
func (s *Service) RequestRework(ctx context.Context, actor *domain.User, requirementID, reason string) error {
	if err := domain.ValidateReworkReason(reason); err != nil {
		return err
	}
	now := s.now()
	return s.store.Tx(ctx, func(tx store.Execer) error {
		req, err := s.store.GetRequirementTx(ctx, tx, requirementID)
		if err != nil {
			return wrapNotFound(err)
		}
		if !actorCanOperate(actor, req) {
			return fmt.Errorf("%w: 只有需求人可以提出重做", domain.ErrForbidden)
		}
		if req.Status != domain.ReqApproved {
			return fmt.Errorf("%w: 当前状态 %s 不允许提出重做", domain.ErrInvalidState, req.Status)
		}
		if err := s.store.UpdateRequirementStatusTx(ctx, tx, req.ID, domain.ReqRejected, req.CurrentTaskID, req.CurrentCandidate, req.AttemptCount, now); err != nil {
			return err
		}
		return s.store.InsertEventTx(ctx, tx, newEvent(actor, req.ID, req.CurrentTaskID, domain.EventReworkRequested,
			fmt.Sprintf("需求人提出重做，原因：%s", reason), now))
	})
}

// Withdraw 需求人在确认采用前撤销需求（题面 R7）。
// 本地撤销不保证供应商停止执行，迟到的成功结果只保留审计，不恢复需求、不形成交付。
func (s *Service) Withdraw(ctx context.Context, actor *domain.User, requirementID string) error {
	now := s.now()
	return s.store.Tx(ctx, func(tx store.Execer) error {
		req, err := s.store.GetRequirementTx(ctx, tx, requirementID)
		if err != nil {
			return wrapNotFound(err)
		}
		if !actorCanOperate(actor, req) {
			return fmt.Errorf("%w: 只有需求人可以撤销需求", domain.ErrForbidden)
		}
		if !domain.CanWithdraw(req.Status) {
			return fmt.Errorf("%w: 当前状态 %s 不允许撤销", domain.ErrInvalidState, req.Status)
		}
		if err := s.store.UpdateRequirementStatusTx(ctx, tx, req.ID, domain.ReqWithdrawn, req.CurrentTaskID, req.CurrentCandidate, req.AttemptCount, now); err != nil {
			return err
		}
		return s.store.InsertEventTx(ctx, tx, newEvent(actor, req.ID, req.CurrentTaskID, domain.EventWithdrawn,
			"需求人在确认采用前撤销需求", now))
	})
}

// AddOperatorNote 运营记录跟进意见（无权代替需求人确认采用）。
func (s *Service) AddOperatorNote(ctx context.Context, actor *domain.User, requirementID, note string) error {
	if actor.Role != domain.RoleOperator {
		return fmt.Errorf("%w: 只有运营可以记录跟进意见", domain.ErrForbidden)
	}
	if err := domain.ValidateOperatorNote(note); err != nil {
		return err
	}
	now := s.now()
	return s.store.Tx(ctx, func(tx store.Execer) error {
		req, err := s.store.GetRequirementTx(ctx, tx, requirementID)
		if err != nil {
			return wrapNotFound(err)
		}
		return s.store.InsertEventTx(ctx, tx, newEvent(actor, req.ID, req.CurrentTaskID, domain.EventOperatorNote,
			fmt.Sprintf("运营跟进意见：%s", note), now))
	})
}

// supplierSubmit 组装提交给供应商的请求。
func supplierSubmit(task *domain.GenerationTask) supplier.SubmitRequest {
	return supplier.SubmitRequest{Ref: task.ID, Prompt: task.DescriptionSnapshot}
}
