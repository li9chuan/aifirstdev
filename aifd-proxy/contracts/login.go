package contracts

import (
	"context"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

// LoginRequest 是 websocket packet 登录时交给业务认证器的框架中立请求。
type LoginRequest struct {
	ConnectionID string
	RemoteAddr   string
	Header       map[string]string
	Message      rpc.Message
}

// LoginResult 是业务登录认证返回给 proxy 的身份和可选回包。
type LoginResult struct {
	Identity           Identity
	Response           *rpc.Message
	CloseAfterResponse bool
}

// PublicRequest 是未登录连接请求公开数据时交给业务处理器的请求。
type PublicRequest struct {
	ConnectionID string
	RemoteAddr   string
	Header       map[string]string
	Message      rpc.Message
}

// PublicResult 是公开请求处理器返回给当前连接的可选回包。
type PublicResult struct {
	Response *rpc.Message
}

// LoginHandler 处理业务登录包；框架不解析业务 PB。
type LoginHandler interface {
	Login(context.Context, LoginRequest) (LoginResult, error)
}

// PublicHandler 处理未登录连接允许访问的公开请求。
type PublicHandler interface {
	HandlePublic(context.Context, PublicRequest) (PublicResult, error)
}

// LoginHandlerFunc 将函数适配为 LoginHandler。
type LoginHandlerFunc func(context.Context, LoginRequest) (LoginResult, error)

// Login 调用被包装的登录函数。
func (f LoginHandlerFunc) Login(ctx context.Context, req LoginRequest) (LoginResult, error) {
	return f(ctx, req)
}

// PublicHandlerFunc 将函数适配为 PublicHandler。
type PublicHandlerFunc func(context.Context, PublicRequest) (PublicResult, error)

// HandlePublic 调用被包装的公开请求处理函数。
func (f PublicHandlerFunc) HandlePublic(ctx context.Context, req PublicRequest) (PublicResult, error) {
	return f(ctx, req)
}

// DownlinkSubscriber 管理已登录 uid 的下行订阅。
type DownlinkSubscriber interface {
	SubscribeUID(uid int64) error
	UnsubscribeUID(uid int64) error
}
