package rpc

import (
	"context"
	"errors"
	"strconv"
	"sync"

	contracts "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	"google.golang.org/protobuf/proto"
)

const errorMessageName = "RPCError"

// Session 表示一次业务 RPC 调用，并作为业务侧唯一出口。
type Session struct {
	ctx       context.Context
	bus       contracts.Bus
	base      contracts.Session
	message   contracts.Message
	mu        sync.Mutex
	responded bool
}

func newSession(ctx context.Context, bus contracts.Bus, base contracts.Session, msg contracts.Message) *Session {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Session{ctx: ctx, bus: bus, base: base, message: msg.Clone()}
}

// Context 返回底层 context；仅用于高级逃生场景。
func (s *Session) Context() context.Context {
	return s.ctx
}

// UID 返回本次调用关联的用户 ID。
func (s *Session) UID() int64 {
	if s == nil || s.base == nil {
		return 0
	}
	return s.base.UID()
}

// Topic 返回本次调用的 route/topic。
func (s *Session) Topic() string {
	if s == nil || s.base == nil {
		return ""
	}
	return s.base.Topic()
}

// TraceID 返回本次调用的 traceID。
func (s *Session) TraceID() string {
	return s.Header(contracts.HeaderTraceID)
}

// ClientInbox 返回前端 await inbox。
func (s *Session) ClientInbox() int {
	if s == nil || s.base == nil {
		return 0
	}
	return s.base.ClientInbox()
}

// Header 按 key 返回本次调用的 header 值。
func (s *Session) Header(key string) string {
	if s == nil || len(s.message.Header) == 0 {
		return ""
	}
	return s.message.Header[key]
}

// Response 完成本次 RPC 的成功响应。
func (s *Session) Response(pb proto.Message) Result {
	return s.ResponseAs("", pb)
}

// ResponseAs 使用指定 topic 完成本次 RPC 的成功响应。
func (s *Session) ResponseAs(topic string, pb proto.Message) Result {
	if pb == nil {
		return failResult(CodeInvalid, "response proto is nil")
	}
	if result := s.markResponded(); !result.OK() {
		return result
	}
	msg, err := contracts.NewMessagePB(topic, pb)
	if err != nil {
		return failResult(CodeInvalid, err.Error())
	}
	s.decorateResponse(&msg)
	if err := s.base.Respond(s.ctx, msg); err != nil {
		return failFromError(err)
	}
	return okResult()
}

// Error 完成本次 RPC 的失败响应。
func (s *Session) Error(code string, message string) Result {
	if code == "" {
		code = CodeInternal
	}
	if result := s.markResponded(); !result.OK() {
		return result
	}
	msg := contracts.Message{Name: errorMessageName, Header: map[string]string{
		contracts.HeaderStatus:       code,
		contracts.HeaderErrorMessage: message,
	}}
	s.decorateResponse(&msg)
	if err := s.base.Respond(s.ctx, msg); err != nil {
		return failFromError(err)
	}
	return okResult()
}

// Push 向当前 UID 推送 PB 消息。
func (s *Session) Push(pb proto.Message) Result {
	return s.PushTo(s.UID(), pb)
}

// PushTo 向指定 UID 推送 PB 消息。
func (s *Session) PushTo(uid int64, pb proto.Message) Result {
	if pb == nil {
		return failResult(CodeInvalid, "push proto is nil")
	}
	msg, err := contracts.NewMessagePB("", pb)
	if err != nil {
		return failResult(CodeInvalid, err.Error())
	}
	s.applyTrace(&msg)
	if err := s.base.Push(s.ctx, uid, msg); err != nil {
		return failFromError(err)
	}
	return okResult()
}

// Tell 发起无回包服务间调用；默认不携带前端 await inbox。
func (s *Session) Tell(topic string, req proto.Message, options ...CallOption) Result {
	msg, result := s.outboundMessage(topic, req, false)
	if !result.OK() {
		return result
	}
	ctx, cancel := s.callContext(options)
	defer cancel()
	if err := s.bus.Tell(ctx, s.UID(), msg); err != nil {
		return failFromError(err)
	}
	return okResult()
}

// Request 发起服务间 await 调用，响应回到当前 handler。
func (s *Session) Request(topic string, req proto.Message, rsp proto.Message, options ...CallOption) Result {
	if rsp == nil {
		return failResult(CodeInvalid, "request response proto is nil")
	}
	msg, result := s.outboundMessage(topic, req, true)
	if !result.OK() {
		return result
	}
	opts := applyCallOptions(options)
	ctx, cancel := s.callContext(options)
	defer cancel()
	response, err := s.bus.Request(ctx, s.UID(), msg, contracts.RequestOptions{Timeout: opts.timeout})
	if err != nil {
		return failFromError(err)
	}
	if err := contracts.StatusFromMessage(response); err != nil {
		var statusErr *contracts.StatusError
		if errors.As(err, &statusErr) {
			return failResult(statusErr.Code, statusErr.Message)
		}
		return failFromError(err)
	}
	if err := response.DecodePB(rsp); err != nil {
		return failResult(CodeDecode, err.Error())
	}
	return okResult()
}

// Forward 将当前 RPC 责任转移给目标 route，并保留前端 await inbox。
func (s *Session) Forward(topic string, req proto.Message, options ...CallOption) Result {
	msg, result := s.outboundMessage(topic, req, true)
	if !result.OK() {
		return result
	}
	if s.ClientInbox() > 0 {
		if result := s.markResponded(); !result.OK() {
			return result
		}
	}
	ctx, cancel := s.callContext(options)
	defer cancel()
	if err := s.bus.Tell(ctx, s.UID(), msg); err != nil {
		return failFromError(err)
	}
	return okResult()
}

func (s *Session) markResponded() Result {
	if s == nil || s.base == nil {
		return failResult(CodeInvalid, "session is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.responded {
		return failResult(CodeDuplicateResponse, "rpc already responded")
	}
	s.responded = true
	return okResult()
}

func (s *Session) decorateResponse(msg *contracts.Message) {
	if msg.Header == nil {
		msg.Header = map[string]string{}
	}
	s.applyTrace(msg)
	if inbox := s.ClientInbox(); inbox > 0 {
		msg.Header[contracts.HeaderClientInbox] = strconv.Itoa(inbox)
	}
	if msg.Header[contracts.HeaderResponse] == "" {
		msg.Header[contracts.HeaderResponse] = msg.Name
	}
}

func (s *Session) applyTrace(msg *contracts.Message) {
	if msg.Header == nil {
		msg.Header = map[string]string{}
	}
	if traceID := s.TraceID(); traceID != "" && msg.Header[contracts.HeaderTraceID] == "" {
		msg.Header[contracts.HeaderTraceID] = traceID
	}
}

func (s *Session) outboundMessage(topic string, pb proto.Message, includeClientInbox bool) (contracts.Message, Result) {
	if topic == "" {
		return contracts.Message{}, failResult(CodeInvalid, "topic is empty")
	}
	if pb == nil {
		return contracts.Message{}, failResult(CodeInvalid, "request proto is nil")
	}
	msg, err := contracts.NewMessagePB(topic, pb)
	if err != nil {
		return contracts.Message{}, failResult(CodeInvalid, err.Error())
	}
	s.applyTrace(&msg)
	if includeClientInbox {
		if inbox := s.ClientInbox(); inbox > 0 {
			msg.Header[contracts.HeaderClientInbox] = strconv.Itoa(inbox)
		}
	}
	return msg, okResult()
}

func (s *Session) callContext(options []CallOption) (context.Context, context.CancelFunc) {
	opts := applyCallOptions(options)
	if opts.timeout > 0 {
		return context.WithTimeout(s.ctx, opts.timeout)
	}
	return s.ctx, func() {}
}

func failFromError(err error) Result {
	if err == nil {
		return okResult()
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return failResult(CodeTimeout, err.Error())
	}
	return failResult(CodeTransport, err.Error())
}
