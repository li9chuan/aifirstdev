package app

import (
	"context"

	counterworld "github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/counter/world"
	observe "github.com/li9chuan/aifirstdev/aifd-observe/contracts"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	runtimeservice "github.com/li9chuan/aifirstdev/aifd-runtime/service"
)

// WorldApp 持有 minigame world 服务和 counter service module。
type WorldApp struct {
	Registry *rpc.Registry
	Service  *runtimeservice.Service
	Counter  *counterworld.Module
}

type worldOptions struct {
	db *rdb.DB
}

// WorldOption 配置 minigame world host。
type WorldOption func(*worldOptions)

// WithWorldDB 传入 global 状态使用的显式 DB。
func WithWorldDB(db *rdb.DB) WorldOption {
	return func(opts *worldOptions) {
		opts.db = db
	}
}

// NewWorldApp 创建 world host，恢复模块并注册内部 RPC。
func NewWorldApp(ctx context.Context, reg *rpc.Registry, sink observe.Sink, options ...WorldOption) (*WorldApp, error) {
	if reg == nil {
		reg = rpc.NewRegistry()
	}
	var opts worldOptions
	for _, option := range options {
		if option != nil {
			option(&opts)
		}
	}
	counterModule := counterworld.NewModule(sink, counterworld.WithDB(opts.db))
	service := runtimeservice.NewService(reg, counterModule)
	if err := service.Recover(ctx); err != nil {
		return nil, err
	}
	if err := service.RegisterRPC(); err != nil {
		return nil, err
	}
	if err := service.Start(ctx); err != nil {
		return nil, err
	}
	return &WorldApp{Registry: reg, Service: service, Counter: counterModule}, nil
}

// Close 停止 world host。
func (a *WorldApp) Close(ctx context.Context) error {
	if a == nil || a.Service == nil {
		return nil
	}
	return a.Service.Close(ctx)
}
