package runtime

import (
	"context"
	"errors"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	"google.golang.org/protobuf/proto"
)

// ErrSessionResponderMissing 表示 session 没有响应或 push hook。
var ErrSessionResponderMissing = errors.New("rpc session responder is missing")

// Session 是 rpc.Session 的 runtime 实现。
type Session struct {
	uid         int64
	topic       string
	inbox       string
	clientInbox int
	respond     func(context.Context, rpc.Message) error
	push        func(context.Context, int64, rpc.Message) error
}

// SessionOptions 配置 runtime session。
type SessionOptions struct {
	UID         int64
	Topic       string
	Inbox       string
	ClientInbox int
	Respond     func(context.Context, rpc.Message) error
	Push        func(context.Context, int64, rpc.Message) error
}

// NewSession 基于不可变请求元数据和 hook 创建 runtime session。
func NewSession(opts SessionOptions) *Session {
	return &Session{
		uid:         opts.UID,
		topic:       opts.Topic,
		inbox:       opts.Inbox,
		clientInbox: opts.ClientInbox,
		respond:     opts.Respond,
		push:        opts.Push,
	}
}

// UID 返回与请求关联的用户 ID。
func (s *Session) UID() int64 {
	return s.uid
}

// Topic 返回创建该 session 的 route 或 subject 名称。
func (s *Session) Topic() string {
	return s.topic
}

// Inbox 返回该 session 的 request/reply inbox。
func (s *Session) Inbox() string {
	return s.inbox
}

// ClientInbox 返回 proxy 请求携带的客户端侧 inbox token。
func (s *Session) ClientInbox() int {
	return s.clientInbox
}

// Respond 通过配置的响应 hook 发送克隆后的响应消息。
func (s *Session) Respond(ctx context.Context, msg rpc.Message) error {
	if s.respond == nil {
		return ErrSessionResponderMissing
	}
	return s.respond(ctx, msg.Clone())
}

// RespondPB marshal protobuf 响应，并通过 Respond 发送。
func (s *Session) RespondPB(ctx context.Context, pb proto.Message) error {
	msg, err := rpc.NewMessagePB("", pb)
	if err != nil {
		return err
	}
	return s.Respond(ctx, msg)
}

// Push 通过配置的 push hook 向目标 uid 发送克隆后的消息。
func (s *Session) Push(ctx context.Context, uid int64, msg rpc.Message) error {
	if s.push == nil {
		return ErrSessionResponderMissing
	}
	return s.push(ctx, uid, msg.Clone())
}

// PushPB marshal protobuf push 消息，并通过 Push 发送。
func (s *Session) PushPB(ctx context.Context, uid int64, pb proto.Message) error {
	msg, err := rpc.NewMessagePB("", pb)
	if err != nil {
		return err
	}
	return s.Push(ctx, uid, msg)
}
