package runtime

import (
	"context"
	"fmt"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

// Dispatch 按消息名查找 route 并调用其 handler。
func (r *Router) Dispatch(ctx context.Context, session rpc.Session, msg rpc.Message) error {
	route, ok := r.Get(msg.Name)
	if !ok {
		return fmt.Errorf("rpc route %s not found", msg.Name)
	}
	if route.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, route.Timeout)
		defer cancel()
	}
	return route.Handler(ctx, session, msg)
}
