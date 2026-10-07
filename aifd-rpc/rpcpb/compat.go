package rpcpb

import (
	"context"
	"time"

	base "github.com/li9chuan/aifirstdev/aifd-contracts"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	contracts "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

const (
	CodeOK                = rpc.CodeOK
	CodeInvalid           = rpc.CodeInvalid
	CodeDecode            = rpc.CodeDecode
	CodeInternal          = rpc.CodeInternal
	CodeTransport         = rpc.CodeTransport
	CodeTimeout           = rpc.CodeTimeout
	CodeUnsupported       = rpc.CodeUnsupported
	CodeDuplicateResponse = rpc.CodeDuplicateResponse
)

// Deprecated: 新代码请使用 github.com/li9chuan/aifirstdev/aifd-rpc.Registry。
type Registry = rpc.Registry

// Deprecated: 新代码请使用 github.com/li9chuan/aifirstdev/aifd-rpc.Session。
type Session = rpc.Session

// Deprecated: 新代码请使用 github.com/li9chuan/aifirstdev/aifd-rpc.Result。
type Result = rpc.Result

// Deprecated: 新代码请使用 github.com/li9chuan/aifirstdev/aifd-rpc.RouteOption。
type RouteOption = rpc.RouteOption

// Deprecated: 新代码请使用 github.com/li9chuan/aifirstdev/aifd-rpc.CallOption。
type CallOption = rpc.CallOption

// NewRegistry 创建空业务 route 注册表。
//
// Deprecated: 新代码请使用 rpc.NewRegistry。
func NewRegistry() *Registry {
	return rpc.NewRegistry()
}

// Client 注册客户端入口 route。
//
// Deprecated: 新代码请使用 rpc.Client。
func Client(reg *Registry, name string, handler any, options ...RouteOption) error {
	return rpc.Client(reg, name, handler, options...)
}

// Server 注册服务间入口 route。
//
// Deprecated: 新代码请使用 rpc.Server。
func Server(reg *Registry, name string, handler any, options ...RouteOption) error {
	return rpc.Server(reg, name, handler, options...)
}

// Admin 注册管理入口 route。
//
// Deprecated: 新代码请使用 rpc.Admin。
func Admin(reg *Registry, name string, handler any, options ...RouteOption) error {
	return rpc.Admin(reg, name, handler, options...)
}

// Stream 注册可靠事件 route。
//
// Deprecated: 新代码请使用 rpc.Stream。
func Stream(reg *Registry, name string, handler any, options ...RouteOption) error {
	return rpc.Stream(reg, name, handler, options...)
}

// Install 将业务注册表安装到真实 Bus。
//
// Deprecated: 新代码请使用 rpc.Install。
func Install(bus contracts.Bus, reg *Registry) error {
	return rpc.Install(bus, reg)
}

// InstallContext 将业务注册表安装到真实 Bus，并为 stream 注册提供 context。
//
// Deprecated: 新代码请使用 rpc.InstallContext。
func InstallContext(ctx context.Context, bus contracts.Bus, reg *Registry) error {
	return rpc.InstallContext(ctx, bus, reg)
}

// WithQueue 设置 queue route 的队列名。
//
// Deprecated: 新代码请使用 rpc.WithQueue。
func WithQueue(queue string) RouteOption {
	return rpc.WithQueue(queue)
}

// WithRouteTimeout 设置 route handler 的执行超时。
//
// Deprecated: 新代码请使用 rpc.WithRouteTimeout。
func WithRouteTimeout(timeout time.Duration) RouteOption {
	return rpc.WithRouteTimeout(timeout)
}

// WithService 设置拥有 route 的服务角色。
//
// Deprecated: 新代码请使用 rpc.WithService。
func WithService(service base.ServiceRole) RouteOption {
	return rpc.WithService(service)
}

// WithTimeout 设置本次服务间调用超时。
//
// Deprecated: 新代码请使用 rpc.WithTimeout。
func WithTimeout(timeout time.Duration) CallOption {
	return rpc.WithTimeout(timeout)
}
