package contracts

import (
	"context"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

// Connection 是 proxy 层拥有的客户端传输连接。
type Connection interface {
	ID() string
	Send(context.Context, rpc.Message) error
	Close(context.Context) error
}

// Envelope 是来自客户端连接的已认证上行消息。
type Envelope struct {
	ConnectionID string
	UID          int64
	Message      rpc.Message
}

// Upstream 将已认证客户端消息转发到下一层服务。
type Upstream interface {
	Forward(context.Context, Envelope) error
}

// UpstreamFunc 将函数适配为 Upstream。
type UpstreamFunc func(context.Context, Envelope) error

// Forward 调用被包装的上行转发函数。
func (f UpstreamFunc) Forward(ctx context.Context, env Envelope) error {
	return f(ctx, env)
}
