package contracts

import "context"

// IssueSeverity 对配置校验输出进行分级。
type IssueSeverity string

const (
	IssueInfo  IssueSeverity = "info"
	IssueWarn  IssueSeverity = "warn"
	IssueError IssueSeverity = "error"
)

// Issue 描述一条校验发现。
type Issue struct {
	Severity IssueSeverity
	Table    string
	Key      string
	Field    string
	Message  string
}

// Validator 在配置包提交前检查强类型配置包。
//
// Validator 必须把 bundle 视为只读对象。补默认值、normalize、remap 和派生索引构建
// 属于 Loader/Builder 职责，应在 Manager 发布前一次性完成。
type Validator[T any] interface {
	Validate(context.Context, *Bundle[T]) []Issue
}

// ValidatorFunc 将函数适配为 Validator。
type ValidatorFunc[T any] func(context.Context, *Bundle[T]) []Issue

// Validate 执行被包装的只读校验函数。
func (fn ValidatorFunc[T]) Validate(ctx context.Context, bundle *Bundle[T]) []Issue {
	return fn(ctx, bundle)
}
