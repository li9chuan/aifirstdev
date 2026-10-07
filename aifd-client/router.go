package client

import (
	"context"
	"sync"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

type pushHandler func(context.Context, rpc.Message) error

type messageRouter struct {
	mu       sync.RWMutex
	handlers map[string][]pushHandler
}

func newMessageRouter() *messageRouter {
	return &messageRouter{handlers: make(map[string][]pushHandler)}
}

func (r *messageRouter) Register(name string, handler pushHandler) {
	if name == "" || handler == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[name] = append(r.handlers[name], handler)
}

func (r *messageRouter) Route(ctx context.Context, msg rpc.Message) (bool, error) {
	r.mu.RLock()
	handlers := append([]pushHandler(nil), r.handlers[msg.Name]...)
	r.mu.RUnlock()
	if len(handlers) == 0 {
		return false, nil
	}
	for _, handler := range handlers {
		if err := handler(ctx, msg); err != nil {
			return true, err
		}
	}
	return true, nil
}
