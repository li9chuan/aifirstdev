package contracts

import "context"

// Settings 承载行为节点参数。
type Settings map[string]any

// Action 是由业务 robot 模块实现的叶子行为。
type Action interface {
	Tick(context.Context, Agent) Status
}

// ActionFunc 将函数适配为 Action。
type ActionFunc func(context.Context, Agent) Status

// Tick 调用包装的 action 函数。
func (f ActionFunc) Tick(ctx context.Context, agent Agent) Status {
	return f(ctx, agent)
}

// ActionFactory 为行为树创建新的 action 实例。
type ActionFactory func(Settings) Action

// ActionRegistration 将行为节点名绑定到 factory。
type ActionRegistration struct {
	Name    string
	Factory ActionFactory
}
