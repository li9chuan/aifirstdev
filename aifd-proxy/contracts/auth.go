package contracts

import (
	"context"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	"time"
)

// AuthRequest 有意保持传输中立；adapter 可将 websocket、TCP 或平台认证元数据映射进来。
type AuthRequest struct {
	Token      string
	RemoteAddr string
	Header     map[string]string
}

// Identity 是 proxy 层已认证主体。
type Identity struct {
	UID        int64
	Account    string
	ServerID   int32
	Claims     map[string]string
	BackendID  string
	SessionID  string
	ValidUntil time.Time
}

// AuthContext is the authenticated connection snapshot forwarded to business RPC.
// Account may use an external string/UUID and SessionID may be empty.
type AuthContext struct {
	BackendID  string
	Account    string
	SessionID  string
	ValidUntil time.Time
}

// AuthRevalidator checks authority state after initial Authenticate succeeds.
// It cannot change identity or extend the original authentication deadline.
// Implementations must honor context cancellation and may be called concurrently.
type AuthRevalidator interface {
	Revalidate(context.Context, AuthContext) error
}

// AuthCheckMode declares connection revalidation semantics independently of sessions.
type AuthCheckMode uint8

const (
	AuthCheckUnspecified AuthCheckMode = iota
	AuthCheckExpiryOnly
	AuthCheckPeriodic
	AuthCheckPerRequest
)

// AuthContext projects verified connection identity, never client-supplied headers.
func (i Identity) AuthContext() AuthContext {
	return AuthContext{BackendID: i.BackendID, Account: i.Account, SessionID: i.SessionID, ValidUntil: i.ValidUntil}
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
