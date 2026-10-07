package rpc

import (
	"time"

	base "github.com/li9chuan/aifirstdev/aifd-contracts"
)

type routeOptions struct {
	queue   string
	timeout time.Duration
	service base.ServiceRole
}

// RouteOption 配置业务 route 注册元数据。
type RouteOption func(*routeOptions)

// WithQueue 设置 queue route 的队列名。
func WithQueue(queue string) RouteOption {
	return func(opts *routeOptions) {
		opts.queue = queue
	}
}

// WithRouteTimeout 设置 route handler 的执行超时。
func WithRouteTimeout(timeout time.Duration) RouteOption {
	return func(opts *routeOptions) {
		opts.timeout = timeout
	}
}

// WithService 设置拥有 route 的服务角色。
func WithService(service base.ServiceRole) RouteOption {
	return func(opts *routeOptions) {
		opts.service = service
	}
}

type callOptions struct {
	timeout time.Duration
}

// CallOption 配置 Session 发起的服务间调用。
type CallOption func(*callOptions)

// WithTimeout 设置本次服务间调用超时。
func WithTimeout(timeout time.Duration) CallOption {
	return func(opts *callOptions) {
		opts.timeout = timeout
	}
}

func applyRouteOptions(options []RouteOption) routeOptions {
	var opts routeOptions
	for _, option := range options {
		if option != nil {
			option(&opts)
		}
	}
	return opts
}

func applyCallOptions(options []CallOption) callOptions {
	var opts callOptions
	for _, option := range options {
		if option != nil {
			option(&opts)
		}
	}
	return opts
}
