package rpc

import (
	"context"

	rpclocal "github.com/li9chuan/aifirstdev/aifd-rpc/adapters/local"
	contracts "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	rpcruntime "github.com/li9chuan/aifirstdev/aifd-rpc/runtime"
	"google.golang.org/protobuf/proto"
)

// Bus 是业务启动层默认使用的 RPC bus 接口。
type Bus = contracts.Bus

// Message 是 RPC 底层消息，主要用于 proxy、robot 和测试入口。
type Message = contracts.Message

// CallOptions 控制异步 request/reply 行为。
type CallOptions = contracts.CallOptions

// RequestOptions 控制同步 request/reply 行为。
type RequestOptions = contracts.RequestOptions

// StatusError reports a framework status response returned by a remote handler.
type StatusError = contracts.StatusError

// LocalBus 是进程内 RPC bus，适合本地开发、单测和 all-in-one 示例。
type LocalBus = rpclocal.Bus

// LocalConfig 控制进程内 RPC bus 的缓冲、超时和统计标签。
type LocalConfig = rpclocal.Config

// RunnerOptions 配置 RPC runner 的 worker 数量和队列深度。
type RunnerOptions = rpcruntime.RunnerOptions

// NewLocalBus 创建进程内 RPC bus。
func NewLocalBus(cfg LocalConfig) *LocalBus {
	return rpclocal.NewBus(cfg)
}

// NewMessagePB marshal protobuf 请求并构造 RPC 消息。
func NewMessagePB(name string, pb proto.Message) (Message, error) {
	return contracts.NewMessagePB(name, pb)
}

// StatusFromMessage converts a status header or a legacy RPCError reply into a typed error.
func StatusFromMessage(msg Message) error {
	return contracts.StatusFromMessage(msg)
}

// Run 阻塞消费 bus deliveries，适合需要自行管理 goroutine 的启动层。
func Run(ctx context.Context, bus Bus, options ...RunnerOptions) {
	if bus == nil {
		return
	}
	rpcruntime.Run(ctx, bus.Deliveries(), options...)
}

// Start 安装业务注册表并在后台启动 runner；bus 生命周期仍由调用方关闭。
func Start(ctx context.Context, bus Bus, reg *Registry, options ...RunnerOptions) error {
	if err := InstallContext(ctx, bus, reg); err != nil {
		return err
	}
	go Run(context.Background(), bus, options...)
	return nil
}
