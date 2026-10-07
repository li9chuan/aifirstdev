package contracts

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	proxycodec "github.com/li9chuan/aifirstdev/aifd-proxy/codec"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	"google.golang.org/protobuf/proto"
)

var ErrInvalidTypedLoginHandler = errors.New("invalid typed login handler")

// LoginContext 是 typed login 交给业务认证函数的连接上下文。
type LoginContext struct {
	ConnectionID string
	RemoteAddr   string
	Header       map[string]string
}

// TypedLoginFunc 是 typed login 的业务认证函数。
type TypedLoginFunc[Req proto.Message, Rsp proto.Message] func(context.Context, LoginContext, Req) (Identity, Rsp)

// DecodeFailureFunc 在登录 PB 解码失败时构造业务失败回包。
type DecodeFailureFunc[Rsp proto.Message] func(context.Context, LoginContext, error) Rsp

// TypedLoginOptions 配置基于 PB 类型的登录处理器。
type TypedLoginOptions[Req proto.Message, Rsp proto.Message] struct {
	LoginTopic        string
	ResultTopic       string
	KeepOpenOnFailure bool
	Handler           TypedLoginFunc[Req, Rsp]
	DecodeFailure     DecodeFailureFunc[Rsp]
}

// TypedLoginHandler 将登录 packet decode 为业务 PB，再调用业务认证函数。
type TypedLoginHandler[Req proto.Message, Rsp proto.Message] struct {
	loginTopic        string
	resultTopic       string
	keepOpenOnFailure bool
	handler           TypedLoginFunc[Req, Rsp]
	decodeFailure     DecodeFailureFunc[Rsp]
	initErr           error
}

// NewLoginHandler 使用默认 topic 推导创建 typed login 处理器。
func NewLoginHandler[Req proto.Message, Rsp proto.Message](handler TypedLoginFunc[Req, Rsp]) *TypedLoginHandler[Req, Rsp] {
	return NewTypedLoginHandler(TypedLoginOptions[Req, Rsp]{Handler: handler})
}

// NewTypedLoginHandler 创建带高级配置的 typed login 处理器。
func NewTypedLoginHandler[Req proto.Message, Rsp proto.Message](opts TypedLoginOptions[Req, Rsp]) *TypedLoginHandler[Req, Rsp] {
	handler := &TypedLoginHandler[Req, Rsp]{
		keepOpenOnFailure: opts.KeepOpenOnFailure,
		handler:           opts.Handler,
		decodeFailure:     opts.DecodeFailure,
	}
	var initErr error
	if opts.Handler == nil {
		initErr = errors.Join(initErr, fmt.Errorf("%w: handler is nil", ErrInvalidTypedLoginHandler))
	}
	if opts.LoginTopic == "" {
		name, err := typedProtoName[Req]()
		if err != nil {
			initErr = errors.Join(initErr, err)
		} else {
			opts.LoginTopic = name
		}
	}
	if opts.LoginTopic != "" {
		if err := proxycodec.ValidateClientTopic(opts.LoginTopic); err != nil {
			initErr = errors.Join(initErr, err)
		}
	}
	if opts.ResultTopic == "" {
		name, err := typedProtoName[Rsp]()
		if err != nil {
			initErr = errors.Join(initErr, err)
		} else {
			opts.ResultTopic = name
		}
	}
	handler.loginTopic = opts.LoginTopic
	handler.resultTopic = opts.ResultTopic
	handler.initErr = initErr
	return handler
}

// Validate 检查 typed login 的 topic 推导和业务认证函数配置。
func (h *TypedLoginHandler[Req, Rsp]) Validate() error {
	if h == nil {
		return fmt.Errorf("%w: handler is nil", ErrInvalidTypedLoginHandler)
	}
	if h.initErr != nil {
		return h.initErr
	}
	if h.handler == nil {
		return fmt.Errorf("%w: handler is nil", ErrInvalidTypedLoginHandler)
	}
	if err := proxycodec.ValidateClientTopic(h.loginTopic); err != nil {
		return err
	}
	if h.resultTopic == "" {
		return fmt.Errorf("%w: result topic is empty", ErrInvalidTypedLoginHandler)
	}
	return nil
}

// Topic 返回登录请求使用的客户端上行协议名。
func (h *TypedLoginHandler[Req, Rsp]) Topic() string {
	if h == nil {
		return ""
	}
	return h.loginTopic
}

// ResultTopic 返回登录响应使用的服务端下行协议名。
func (h *TypedLoginHandler[Req, Rsp]) ResultTopic() string {
	if h == nil {
		return ""
	}
	return h.resultTopic
}

// Login 实现 LoginHandler。
func (h *TypedLoginHandler[Req, Rsp]) Login(ctx context.Context, req LoginRequest) (LoginResult, error) {
	if err := h.Validate(); err != nil {
		return LoginResult{CloseAfterResponse: true}, err
	}
	if req.Message.Name != h.loginTopic {
		return LoginResult{CloseAfterResponse: true}, fmt.Errorf("%w: unexpected login topic %q", ErrInvalidTypedLoginHandler, req.Message.Name)
	}
	loginCtx := LoginContext{
		ConnectionID: req.ConnectionID,
		RemoteAddr:   req.RemoteAddr,
		Header:       req.Header,
	}
	loginReq, err := newTypedProtoMessage[Req]()
	if err != nil {
		return LoginResult{CloseAfterResponse: true}, err
	}
	if err := req.Message.DecodePB(loginReq); err != nil {
		if h.decodeFailure == nil {
			return LoginResult{CloseAfterResponse: true}, err
		}
		rsp := h.decodeFailure(ctx, loginCtx, err)
		return h.resultFrom(Identity{}, rsp), nil
	}
	identity, rsp := h.handler(ctx, loginCtx, loginReq)
	return h.resultFrom(identity, rsp), nil
}

func (h *TypedLoginHandler[Req, Rsp]) resultFrom(identity Identity, rsp Rsp) LoginResult {
	result := LoginResult{
		Identity:           identity,
		CloseAfterResponse: !h.keepOpenOnFailure && identity.UID <= 0,
	}
	if typedProtoMessageIsNil(rsp) {
		return result
	}
	msg, err := rpc.NewMessagePB(h.resultTopic, rsp)
	if err != nil {
		result.Identity = Identity{}
		result.CloseAfterResponse = true
		return result
	}
	result.Response = &msg
	return result
}

func typedProtoName[T proto.Message]() (string, error) {
	msg, err := newTypedProtoMessage[T]()
	if err != nil {
		return "", err
	}
	return rpc.ProtoName(msg)
}

func newTypedProtoMessage[T proto.Message]() (T, error) {
	var zero T
	typ := reflect.TypeOf(zero)
	if typ == nil || typ.Kind() != reflect.Ptr {
		return zero, fmt.Errorf("%w: proto type must be a concrete pointer", ErrInvalidTypedLoginHandler)
	}
	value := reflect.New(typ.Elem()).Interface()
	msg, ok := value.(T)
	if !ok {
		return zero, fmt.Errorf("%w: proto type %s cannot be constructed", ErrInvalidTypedLoginHandler, typ)
	}
	return msg, nil
}

func typedProtoMessageIsNil(pb proto.Message) bool {
	if pb == nil {
		return true
	}
	value := reflect.ValueOf(pb)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
