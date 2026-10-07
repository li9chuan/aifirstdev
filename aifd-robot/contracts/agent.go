package contracts

import (
	"context"

	"google.golang.org/protobuf/proto"
)

// Agent 是暴露给业务 robot 模块和 action 的 robot 侧视图。
type Agent interface {
	ID() string
	UID() int64
	Account() string
	IsLoggedIn() bool
	Call(proto.Message, ...any) error
	On(any) error
}

// AgentModule 是 robot 侧 feature 行为的业务扩展点。
type AgentModule interface {
	Name() string
	OnAttach(context.Context, Agent) error
	OnLogin(context.Context, Agent) error
	Tick(context.Context, Agent) error
	Actions() []ActionRegistration
}
