// Package domain 定义营销素材 AI 生图场景中的纯类型、状态枚举与规则判定。
// 本包不依赖任何其他内部包，只放实体、状态与可单测的纯函数。
package domain

import "time"

// Role 是系统中的三类主体：需求人、设计复核人、运营。
type Role string

const (
	// RoleRequester 需求人：创建需求、修改描述、发起生成、确认采用、提出重做、撤销。
	RoleRequester Role = "requester"
	// RoleReviewer 设计复核人：审核最新候选图片，给出通过或不通过及原因。
	RoleReviewer Role = "reviewer"
	// RoleOperator 运营：查看生成记录与异常记录，跟进并记录处理意见，无权代替需求人确认采用。
	RoleOperator Role = "operator"
)

// Valid 判断角色是否属于本题约定的三类主体。
func (r Role) Valid() bool {
	switch r {
	case RoleRequester, RoleReviewer, RoleOperator:
		return true
	}
	return false
}

// RequirementStatus 是素材需求的状态机取值。
type RequirementStatus string

const (
	// ReqDraft 已创建、尚未发起生成。
	ReqDraft RequirementStatus = "draft"
	// ReqGenerating 存在一个处理中的生成任务。
	ReqGenerating RequirementStatus = "generating"
	// ReqCandidatePending 最新一次生成成功，等待设计复核人审核。
	ReqCandidatePending RequirementStatus = "candidate_pending"
	// ReqRejected 复核未通过，或需求人对已通过的结果提出重做原因，等待重新生成。
	ReqRejected RequirementStatus = "rejected"
	// ReqApproved 复核通过，等待需求人确认采用或提出重做原因。
	ReqApproved RequirementStatus = "approved"
	// ReqDelivered 需求人已确认采用，交付完成。
	ReqDelivered RequirementStatus = "delivered"
	// ReqFailed 最近一次生成被拒绝、失败或超时，可修改描述后再次发起。
	ReqFailed RequirementStatus = "failed"
	// ReqExhausted 已发起三次生成且最近一次未成功，不可再次发起。
	ReqExhausted RequirementStatus = "exhausted"
	// ReqWithdrawn 需求人在确认采用前撤销需求。
	ReqWithdrawn RequirementStatus = "withdrawn"
)

// TaskStatus 是供应商生成任务的状态取值。
type TaskStatus string

const (
	// TaskProcessing 已提交给供应商、尚未收到有效终态。
	TaskProcessing TaskStatus = "processing"
	// TaskSucceeded 供应商报告成功。
	TaskSucceeded TaskStatus = "succeeded"
	// TaskFailed 供应商报告处理失败。
	TaskFailed TaskStatus = "failed"
	// TaskRejected 模拟提交明确返回拒绝。
	TaskRejected TaskStatus = "rejected"
	// TaskTimeout 在规定时限内没有收到有效终态。
	TaskTimeout TaskStatus = "timeout"
)

// Terminal 判断任务是否已进入终态。终态任务不会被迟到或冲突回调改写。
func (s TaskStatus) Terminal() bool {
	switch s {
	case TaskSucceeded, TaskFailed, TaskRejected, TaskTimeout:
		return true
	}
	return false
}

// ReviewStatus 是候选图片的复核状态取值。
type ReviewStatus string

const (
	// ReviewPending 待复核。
	ReviewPending ReviewStatus = "pending"
	// ReviewApproved 复核通过。
	ReviewApproved ReviewStatus = "approved"
	// ReviewRejected 复核未通过，必须记录原因。
	ReviewRejected ReviewStatus = "rejected"
)

// User 是系统内的预置账号。
type User struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Role         Role   `json:"role"`
	PasswordHash string `json:"password_hash"`
	Salt         string `json:"salt"`
}

// Requirement 是营销素材需求。
type Requirement struct {
	ID               string            `json:"id"`
	OwnerID          string            `json:"owner_id"`
	ActivityName     string            `json:"activity_name"`
	Purpose          string            `json:"purpose"`
	Description      string            `json:"description"`
	Status           RequirementStatus `json:"status"`
	CurrentTaskID    string            `json:"current_task_id"`
	CurrentCandidate string            `json:"current_candidate"`
	AttemptCount     int               `json:"attempt_count"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// GenerationTask 是一次向供应商发起的生成任务，DescriptionSnapshot 是发起当时的图片描述。
type GenerationTask struct {
	ID                  string     `json:"id"`
	RequirementID       string     `json:"requirement_id"`
	RequestNo           string     `json:"request_no"`
	AttemptNo           int        `json:"attempt_no"`
	DescriptionSnapshot string     `json:"description_snapshot"`
	Status              TaskStatus `json:"status"`
	SupplierTaskID      string     `json:"supplier_task_id"`
	ImageURL            string     `json:"image_url"`
	FailureReason       string     `json:"failure_reason"`
	CreatedAt           time.Time  `json:"created_at"`
	FinishedAt          time.Time  `json:"finished_at"`
}

// Candidate 是一次生成产出的候选交付物。
type Candidate struct {
	ID            string       `json:"id"`
	TaskID        string       `json:"task_id"`
	RequirementID string       `json:"requirement_id"`
	AttemptNo     int          `json:"attempt_no"`
	ImageURL      string       `json:"image_url"`
	ReviewStatus  ReviewStatus `json:"review_status"`
	ReviewReason  string       `json:"review_reason"`
	ReviewerID    string       `json:"reviewer_id"`
	ReviewedAt    time.Time    `json:"reviewed_at"`
	IsCurrent     bool         `json:"is_current"`
	CreatedAt     time.Time    `json:"created_at"`
}

// Event 是审计流水，记录每一次状态变更与异常。
type Event struct {
	ID            int64     `json:"id"`
	RequirementID string    `json:"requirement_id"`
	TaskID        string    `json:"task_id"`
	ActorID       string    `json:"actor_id"`
	ActorRole     Role      `json:"actor_role"`
	Kind          string    `json:"kind"`
	Detail        string    `json:"detail"`
	CreatedAt     time.Time `json:"created_at"`
}

// 审计事件类型。
const (
	EventRequirementCreated = "requirement_created"
	EventDescriptionUpdated = "description_updated"
	EventGenerationStarted  = "generation_started"
	EventGenerationRejected = "generation_rejected"
	EventGenerationFailed   = "generation_failed"
	EventGenerationTimeout  = "generation_timeout"
	EventCallbackAccepted   = "callback_accepted"
	EventCallbackIgnored    = "callback_ignored"
	EventCallbackUnknown    = "callback_unknown_task"
	EventCandidateCreated   = "candidate_created"
	EventCandidateReviewed  = "candidate_reviewed"
	EventReworkRequested    = "rework_requested"
	EventAdopted            = "requirement_adopted"
	EventWithdrawn          = "requirement_withdrawn"
	EventOperatorNote       = "operator_note"
)

// 异常事件类型（运营异常查询按此过滤）。
const (
	EventCallbackDuplicate = "callback_duplicate"
	EventCallbackConflict  = "callback_conflict"
	EventLateResultIgnored = "late_result_ignored"
	EventSignatureInvalid  = "signature_invalid"
)

// Clock 抽象当前时间，便于测试与教学时钟演示。
type Clock interface {
	Now() time.Time
}

// SystemClock 使用真实系统时间。
type SystemClock struct{}

// Now 返回当前系统时间。
func (SystemClock) Now() time.Time { return time.Now() }

// FixedClock 返回固定时间，用于单元测试。
type FixedClock struct{ T time.Time }

// Now 返回固定时间。
func (c FixedClock) Now() time.Time { return c.T }
