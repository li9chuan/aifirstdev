package contracts

import (
	"context"

	"google.golang.org/protobuf/proto"
)

// Session 只暴露请求身份和响应基础能力。
type Session interface {
	UID() int64
	Topic() string
	Inbox() string
	ClientInbox() int
	Respond(context.Context, Message) error
	RespondPB(context.Context, proto.Message) error
	Push(context.Context, int64, Message) error
	PushPB(context.Context, int64, proto.Message) error
}

// Handler 处理单条 RPC 消息。
type Handler func(context.Context, Session, Message) error
