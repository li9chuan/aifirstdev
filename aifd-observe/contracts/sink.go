package contracts

import "context"

// Sink 接收批量 observability 事件。
type Sink interface {
	Write(context.Context, []Event) error
}

// SinkFunc 将函数适配为 Sink。
type SinkFunc func(context.Context, []Event) error

// Write 调用被包装的 Sink 函数。
func (f SinkFunc) Write(ctx context.Context, events []Event) error {
	return f(ctx, events)
}
