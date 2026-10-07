package contracts

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"
)

// CallOptions 控制异步 request/reply 行为。
type CallOptions struct {
	Timeout   time.Duration
	OnTimeout func(context.Context) error
}

// RequestOptions 控制同步 request/reply 行为。
type RequestOptions struct {
	Timeout time.Duration
}

// Delivery 是由 bus adapter 创建的可执行 RPC 投递。
type Delivery interface {
	Handle(context.Context) error
	Session() Session
	Message() Message
	Route() Route
}

// Bus 是 runtime、proxy、robot、adapter 和服务启动层使用的底层 RPC 接口。
type Bus interface {
	Register(Route) error
	RegisterQueue(Route, string) error
	Tell(context.Context, int64, Message) error
	TellPB(context.Context, int64, string, proto.Message) error
	Call(context.Context, int64, Message, Handler, CallOptions) error
	CallPB(context.Context, int64, string, proto.Message, Handler, CallOptions) error
	Request(context.Context, int64, Message, RequestOptions) (Message, error)
	RequestPB(context.Context, int64, string, proto.Message, proto.Message, RequestOptions) error
	Push(context.Context, int64, Message) error
	PushPB(context.Context, int64, proto.Message) error
	Deliveries() <-chan Delivery
	Stats() Stats
	Close(context.Context) error
}

// StreamBus 由支持可靠 stream 事件的 bus 实现。
type StreamBus interface {
	RegisterStream(context.Context, Route) error
	SaveToStream(context.Context, int64, Message) error
	SavePBToStream(context.Context, int64, string, proto.Message) error
}
