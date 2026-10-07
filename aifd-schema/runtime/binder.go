package runtime

import (
	"context"
	"fmt"
	"time"

	base "github.com/li9chuan/aifirstdev/aifd-contracts"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	schema "github.com/li9chuan/aifirstdev/aifd-schema/contracts"
)

// HandlerFactory 为单个 schema 消息创建 RPC handler。
type HandlerFactory func(schema.MessageDescriptor) rpc.Handler

// KindMapper 将 schema 流向映射到 RPC route 类型；返回 false 表示跳过不应注册为入站 route 的消息，例如服务端推送。
type KindMapper func(schema.Direction) (rpc.RouteKind, bool)

// BindOptions 配置生成或手写的 schema 到 RPC 绑定。
type BindOptions struct {
	Service        base.ServiceRole
	Timeout        time.Duration
	Queue          string
	HandlerFactory HandlerFactory
	KindMapper     KindMapper
}

// BindRoutes 将入站 schema 消息注册为 RPC route。
func BindRoutes(source schema.Registry, target rpc.Registry, opts BindOptions) error {
	if source == nil {
		return fmt.Errorf("schema source registry is nil")
	}
	if target == nil {
		return fmt.Errorf("rpc target registry is nil")
	}
	if opts.HandlerFactory == nil {
		return fmt.Errorf("schema bind handler factory is nil")
	}
	mapKind := opts.KindMapper
	if mapKind == nil {
		mapKind = DefaultKindMapper
	}
	for _, desc := range source.Messages() {
		kind, ok := mapKind(desc.Direction)
		if !ok {
			continue
		}
		handler := opts.HandlerFactory(desc)
		if handler == nil {
			return fmt.Errorf("schema message %s handler is nil", desc.Name)
		}
		if err := target.Register(rpc.Route{
			Name:    desc.Name,
			Kind:    kind,
			Service: opts.Service,
			Handler: handler,
			Queue:   opts.Queue,
			Timeout: opts.Timeout,
		}); err != nil {
			return err
		}
	}
	return nil
}

// DefaultKindMapper 将常见消息流向映射为入站 RPC route 类型。
func DefaultKindMapper(direction schema.Direction) (rpc.RouteKind, bool) {
	switch direction {
	case schema.DirectionClientToServer:
		return rpc.RouteKindClient, true
	case schema.DirectionServerToServer:
		return rpc.RouteKindServer, true
	case schema.DirectionAdmin:
		return rpc.RouteKindAdmin, true
	default:
		return "", false
	}
}

// NoopHandler 适合在业务代码接入前作为生成骨架的占位 handler。
func NoopHandler(context.Context, rpc.Session, rpc.Message) error {
	return nil
}
