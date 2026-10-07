package local

import (
	"context"
	"sync"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	"google.golang.org/protobuf/proto"
)

// Session 是供 feature 和 router 单元测试使用的 local 测试 session。
type Session struct {
	uid         int64
	topic       string
	inbox       string
	clientInbox int
	mu          sync.Mutex
	responses   []rpc.Message
	pushes      []PushedMessage
}

// PushedMessage 记录 local push 的目标和 payload。
type PushedMessage struct {
	UID     int64
	Message rpc.Message
}

// NewSession 为指定 uid 和 topic 创建 local 测试 session。
func NewSession(uid int64, topic string) *Session {
	return &Session{uid: uid, topic: topic}
}

// UID 返回 session 用户 ID。
func (s *Session) UID() int64 {
	return s.uid
}

// Topic 返回与 session 关联的 route topic。
func (s *Session) Topic() string {
	return s.topic
}

// Inbox 返回 session 的 request/reply inbox。
func (s *Session) Inbox() string {
	return s.inbox
}

// ClientInbox 返回 session 的 proxy 客户端 inbox token。
func (s *Session) ClientInbox() int {
	return s.clientInbox
}

// Respond 记录克隆后的响应消息。
func (s *Session) Respond(_ context.Context, msg rpc.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses = append(s.responses, msg.Clone())
	return nil
}

// RespondPB marshal protobuf 响应，并通过 Respond 记录。
func (s *Session) RespondPB(ctx context.Context, pb proto.Message) error {
	msg, err := rpc.NewMessagePB("", pb)
	if err != nil {
		return err
	}
	return s.Respond(ctx, msg)
}

// Push 为目标 uid 记录克隆后的 push 消息。
func (s *Session) Push(_ context.Context, uid int64, msg rpc.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pushes = append(s.pushes, PushedMessage{UID: uid, Message: msg.Clone()})
	return nil
}

// PushPB marshal protobuf push 消息，并通过 Push 记录。
func (s *Session) PushPB(ctx context.Context, uid int64, pb proto.Message) error {
	msg, err := rpc.NewMessagePB("", pb)
	if err != nil {
		return err
	}
	return s.Push(ctx, uid, msg)
}

// Responses 返回 Respond 记录的响应消息克隆。
func (s *Session) Responses() []rpc.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]rpc.Message, len(s.responses))
	for i := range s.responses {
		out[i] = s.responses[i].Clone()
	}
	return out
}

// Pushes 返回 Push 记录的 push 消息克隆。
func (s *Session) Pushes() []PushedMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PushedMessage, len(s.pushes))
	for i := range s.pushes {
		out[i] = PushedMessage{UID: s.pushes[i].UID, Message: s.pushes[i].Message.Clone()}
	}
	return out
}
