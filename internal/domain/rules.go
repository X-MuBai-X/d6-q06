package domain

import (
	"fmt"
	"strings"
	"time"
)

// MaxAttempts 是单个素材需求允许发起的生成次数上限（题面 R3：最多三次）。
const MaxAttempts = 3

// GenerationTimeout 是题面 R3 规定的「60 秒内无有效终态」上限（真实业务口径）。
const GenerationTimeout = 60 * time.Second

// 字段长度上限，属于题面未明确的实现约定，记录为假设 A2。
const (
	MaxActivityNameLen = 120
	MaxPurposeLen      = 400
	MaxDescriptionLen  = 2000
	MinDescriptionLen  = 4
)

// CanStartGeneration 判断需求当前状态是否允许发起新的生成任务。
// draft：首次发起；failed/rejected：失败、超时、复核未通过后的再次生成；
// approved：复核通过但需求人提出重做原因后重新生成。
func CanStartGeneration(status RequirementStatus) bool {
	switch status {
	case ReqDraft, ReqFailed, ReqRejected, ReqApproved:
		return true
	}
	return false
}

// CanReview 判断需求当前状态是否允许设计复核人给出复核结论。
func CanReview(status RequirementStatus) bool {
	return status == ReqCandidatePending
}

// CanAdopt 判断需求当前状态是否允许需求人确认采用。
func CanAdopt(status RequirementStatus) bool {
	return status == ReqApproved
}

// CanWithdraw 判断需求当前状态是否允许需求人撤销。
// 已确认采用（delivered）与已撤销（withdrawn）不可再次撤销；已耗尽（exhausted）同样不再受理。
func CanWithdraw(status RequirementStatus) bool {
	switch status {
	case ReqDraft, ReqGenerating, ReqCandidatePending, ReqRejected, ReqApproved, ReqFailed, ReqExhausted:
		return true
	}
	return false
}

// NextStatusAfterAttemptFailure 计算一次未成功的生成之后需求应处的状态。
// 达到次数上限时进入 exhausted，否则进入 failed 以便再次发起。
func NextStatusAfterAttemptFailure(attemptNo int) RequirementStatus {
	if attemptNo >= MaxAttempts {
		return ReqExhausted
	}
	return ReqFailed
}

// ValidateActivityName 校验活动名称。
func ValidateActivityName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return fmt.Errorf("%w: 活动名称不能为空", ErrInvalidInput)
	}
	if len([]rune(trimmed)) > MaxActivityNameLen {
		return fmt.Errorf("%w: 活动名称不超过 %d 个字符", ErrInvalidInput, MaxActivityNameLen)
	}
	return nil
}

// ValidatePurpose 校验用途。
func ValidatePurpose(purpose string) error {
	trimmed := strings.TrimSpace(purpose)
	if trimmed == "" {
		return fmt.Errorf("%w: 用途不能为空", ErrInvalidInput)
	}
	if len([]rune(trimmed)) > MaxPurposeLen {
		return fmt.Errorf("%w: 用途不超过 %d 个字符", ErrInvalidInput, MaxPurposeLen)
	}
	return nil
}

// ValidateDescription 校验图片描述。
func ValidateDescription(description string) error {
	trimmed := strings.TrimSpace(description)
	if len([]rune(trimmed)) < MinDescriptionLen {
		return fmt.Errorf("%w: 图片描述至少 %d 个字符", ErrInvalidInput, MinDescriptionLen)
	}
	if len([]rune(trimmed)) > MaxDescriptionLen {
		return fmt.Errorf("%w: 图片描述不超过 %d 个字符", ErrInvalidInput, MaxDescriptionLen)
	}
	return nil
}

// ValidateRequestNo 校验请求编号（幂等键）。
func ValidateRequestNo(requestNo string) error {
	trimmed := strings.TrimSpace(requestNo)
	if trimmed == "" {
		return fmt.Errorf("%w: 请求编号不能为空", ErrInvalidInput)
	}
	if len(trimmed) > 64 {
		return fmt.Errorf("%w: 请求编号不超过 64 个字符", ErrInvalidInput)
	}
	return nil
}

// ValidateReviewReason 校验复核不通过的原因（题面 R5：未通过须写原因）。
func ValidateReviewReason(reason string) error {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return fmt.Errorf("%w: 复核不通过必须填写原因", ErrInvalidInput)
	}
	if len([]rune(trimmed)) > 500 {
		return fmt.Errorf("%w: 原因不超过 500 个字符", ErrInvalidInput)
	}
	return nil
}

// ValidateReworkReason 校验需求人提出的重做原因。
func ValidateReworkReason(reason string) error {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return fmt.Errorf("%w: 重做必须填写原因", ErrInvalidInput)
	}
	if len([]rune(trimmed)) > 500 {
		return fmt.Errorf("%w: 原因不超过 500 个字符", ErrInvalidInput)
	}
	return nil
}

// ValidateOperatorNote 校验运营跟进意见。
func ValidateOperatorNote(note string) error {
	trimmed := strings.TrimSpace(note)
	if trimmed == "" {
		return fmt.Errorf("%w: 跟进意见不能为空", ErrInvalidInput)
	}
	if len([]rune(trimmed)) > 1000 {
		return fmt.Errorf("%w: 跟进意见不超过 1000 个字符", ErrInvalidInput)
	}
	return nil
}

// EffectiveTimeout 按教学时钟倍率返回实际生效的超时上限。
// scale 为 1 时等于题面规定的 60 秒；演示时可用更小的倍率缩短时长（假设 A3）。
func EffectiveTimeout(scale float64) time.Duration {
	if scale <= 0 {
		return GenerationTimeout
	}
	return time.Duration(float64(GenerationTimeout) * scale)
}

// IsAnomalyEvent 判断事件是否属于运营需要跟进的异常类型。
func IsAnomalyEvent(kind string) bool {
	switch kind {
	case EventCallbackDuplicate, EventCallbackConflict, EventLateResultIgnored,
		EventSignatureInvalid, EventCallbackUnknown, EventGenerationRejected,
		EventGenerationFailed, EventGenerationTimeout:
		return true
	}
	return false
}

// StatusLabels 是页面展示用的中文状态名。
func StatusLabels() map[RequirementStatus]string {
	return map[RequirementStatus]string{
		ReqDraft:            "待发起生成",
		ReqGenerating:       "生成中",
		ReqCandidatePending: "待设计复核",
		ReqRejected:         "复核未通过／待重做",
		ReqApproved:         "复核通过／待确认采用",
		ReqDelivered:        "已确认采用（交付完成）",
		ReqFailed:           "生成失败或超时，可再次发起",
		ReqExhausted:        "生成次数已用尽",
		ReqWithdrawn:        "已撤销",
	}
}
