package runtime

import (
	"context"
	"errors"
	"strconv"
	"time"

	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

// RPCUpstream 将已认证客户端消息转发到 aifd-rpc Bus。
type RPCUpstream struct {
	Bus      rpc.Bus
	Delivery *Delivery
	Timeout  time.Duration
}

// NewRPCUpstream 创建使用指定 Bus 的上行转发器。
func NewRPCUpstream(bus rpc.Bus, delivery *Delivery) *RPCUpstream {
	return &RPCUpstream{Bus: bus, Delivery: delivery, Timeout: 15 * time.Second}
}

// Forward 将 Envelope 中的消息按 uid 投递到 RPC route。
func (u *RPCUpstream) Forward(ctx context.Context, env proxy.Envelope) error {
	if u == nil || u.Bus == nil {
		return ErrUpstreamMissing
	}
	inbox, err := strconv.Atoi(env.Message.Header[rpc.HeaderClientInbox])
	if err == nil && inbox > 0 {
		if u.Delivery == nil || env.ConnectionID == "" {
			return ErrConnectionNotFound
		}
		rsp, err := u.Bus.Request(ctx, env.UID, env.Message, rpc.RequestOptions{Timeout: u.Timeout})
		if err != nil && rsp.Name == "" {
			rsp = requestError(err)
		}
		if rsp.Header == nil {
			rsp.Header = make(map[string]string)
		}
		rsp.Header[rpc.HeaderClientInbox] = strconv.Itoa(inbox)
		return u.Delivery.Send(ctx, env.ConnectionID, rsp)
	}
	return u.Bus.Tell(ctx, env.UID, env.Message)
}

func requestError(err error) rpc.Message {
	code := "INTERNAL"
	var status *rpc.StatusError
	if errors.As(err, &status) {
		code = status.Code
	} else if errors.Is(err, context.DeadlineExceeded) {
		code = "TIMEOUT"
	} else if errors.Is(err, context.Canceled) {
		code = "CANCELLED"
	}
	return rpc.Message{Name: "RPCError", Header: map[string]string{rpc.HeaderStatus: code, rpc.HeaderErrorMessage: err.Error()}}
}
