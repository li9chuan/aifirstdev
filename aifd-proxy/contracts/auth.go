package contracts

import "context"

// AuthRequest 有意保持传输中立；adapter 可将 websocket、TCP 或平台认证元数据映射进来。
type AuthRequest struct {
	Token      string
	RemoteAddr string
	Header     map[string]string
}

// Identity 是 proxy 层已认证主体。
type Identity struct {
	UID      int64
	Account  string
	ServerID int32
	Claims   map[string]string
}

// Authenticator 在连接向 game 服务转发消息前认证客户端。
type Authenticator interface {
	Authenticate(context.Context, AuthRequest) (Identity, error)
}

// AuthenticatorFunc 将函数适配为 Authenticator。
type AuthenticatorFunc func(context.Context, AuthRequest) (Identity, error)

// Authenticate 调用被包装的认证函数。
func (f AuthenticatorFunc) Authenticate(ctx context.Context, req AuthRequest) (Identity, error) {
	return f(ctx, req)
}
