package contracts

import (
	"context"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

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

// HandshakeResult selects pre-upgrade authentication. Handled=false permits the
// packet login path; an authentication error must never downgrade to that path.
type HandshakeResult struct {
	Handled        bool
	Identity       Identity
	InitialMessage *rpc.Message
}

// HandshakeHandler authenticates HTTP credentials before the WebSocket upgrade.
type HandshakeHandler interface {
	AuthenticateHandshake(context.Context, AuthRequest) (HandshakeResult, error)
}

// HandshakeHandlerFunc adapts a business HTTP credential verifier.
type HandshakeHandlerFunc func(context.Context, AuthRequest) (HandshakeResult, error)

func (f HandshakeHandlerFunc) AuthenticateHandshake(ctx context.Context, req AuthRequest) (HandshakeResult, error) {
	return f(ctx, req)
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
