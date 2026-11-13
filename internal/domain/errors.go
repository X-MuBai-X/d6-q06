package domain

import "errors"

// 本包定义领域层哨兵错误，HTTP 层据此映射状态码，服务层用 errors.Is 判断。
var (
	// ErrNotFound 对象不存在。
	ErrNotFound = errors.New("对象不存在")
	// ErrForbidden 当前角色无权执行该操作。
	ErrForbidden = errors.New("无权执行该操作")
	// ErrUnauthorized 缺少或无效的会话令牌。
	ErrUnauthorized = errors.New("未登录或会话已失效")
	// ErrInvalidState 当前状态不允许该操作。
	ErrInvalidState = errors.New("当前状态不允许该操作")
	// ErrInvalidInput 调用方可以自行纠正的参数问题。
	ErrInvalidInput = errors.New("参数不合法")
	// ErrConflict 并发或重复操作冲突。
	ErrConflict = errors.New("操作冲突")
	// ErrTooManyAttempts 生成次数已达上限。
	ErrTooManyAttempts = errors.New("生成次数已达上限")
	// ErrNotCurrentCandidate 操作对象不是当前候选。
	ErrNotCurrentCandidate = errors.New("该候选已不是当前候选")
	// ErrUnknownTask 回调关联的供应商任务不存在。
	ErrUnknownTask = errors.New("未知的供应商任务")
	// ErrTaskAlreadyFinished 任务已终结，迟到或冲突回调不再改写。
	ErrTaskAlreadyFinished = errors.New("生成任务已终结")
)
