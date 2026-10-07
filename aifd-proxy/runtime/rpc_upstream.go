package runtime

import (
	"context"

	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

// RPCUpstream 将已认证客户端消息转发到 aifd-rpc Bus。
type RPCUpstream struct {
	Bus rpc.Bus
}

// NewRPCUpstream 创建使用指定 Bus 的上行转发器。
func NewRPCUpstream(bus rpc.Bus) *RPCUpstream {
	return &RPCUpstream{Bus: bus}
}

// Forward 将 Envelope 中的消息按 uid 投递到 RPC route。
func (u *RPCUpstream) Forward(ctx context.Context, env proxy.Envelope) error {
	if u == nil || u.Bus == nil {
		return ErrUpstreamMissing
	}
	return u.Bus.Tell(ctx, env.UID, env.Message)
}
