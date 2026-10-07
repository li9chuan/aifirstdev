package runtime

import (
	"context"
	"fmt"
	"sort"
	"sync"

	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

// Gateway 拥有已认证客户端 Session，并转发上行消息；它不了解 Player、World、DBPlayer 或 feature Module。
type Gateway struct {
	mu           sync.RWMutex
	auth         proxy.Authenticator
	upstream     proxy.Upstream
	byConn       map[string]*Session
	connIDsByUID map[int64]map[string]struct{}
}

// Session 是 proxy 连接级 runtime 状态。
type Session struct {
	ConnectionID string
	Identity     proxy.Identity
}

// NewGateway 创建带空连接索引和 UID 索引的 proxy Gateway。
func NewGateway(auth proxy.Authenticator, upstream proxy.Upstream) *Gateway {
	return &Gateway{
		auth:         auth,
		upstream:     upstream,
		byConn:       make(map[string]*Session),
		connIDsByUID: make(map[int64]map[string]struct{}),
	}
}

// Accept 认证并注册一个连接。
func (g *Gateway) Accept(ctx context.Context, conn proxy.Connection, req proxy.AuthRequest) (Session, error) {
	if g.auth == nil {
		return Session{}, fmt.Errorf("proxy authenticator is missing")
	}
	identity, err := g.auth.Authenticate(ctx, req)
	if err != nil {
		return Session{}, err
	}
	return g.AcceptIdentity(conn, identity)
}

// AcceptIdentity 直接按已认证身份注册连接。
func (g *Gateway) AcceptIdentity(conn proxy.Connection, identity proxy.Identity) (Session, error) {
	if conn == nil || conn.ID() == "" {
		return Session{}, fmt.Errorf("%w: empty connection id", ErrConnectionNotFound)
	}
	if identity.UID <= 0 {
		return Session{}, ErrInvalidIdentity
	}
	session := &Session{ConnectionID: conn.ID(), Identity: identity}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.byConn[conn.ID()]; exists {
		return Session{}, ErrConnectionExists
	}
	g.byConn[conn.ID()] = session
	if g.connIDsByUID[identity.UID] == nil {
		g.connIDsByUID[identity.UID] = make(map[string]struct{})
	}
	g.connIDsByUID[identity.UID][conn.ID()] = struct{}{}
	return *session, nil
}

// Disconnect 从 Session 注册表移除一个连接。
func (g *Gateway) Disconnect(connID string) bool {
	_, ok := g.DisconnectSession(connID)
	return ok
}

// DisconnectSession 从 Session 注册表移除一个连接，并返回被移除的 Session。
func (g *Gateway) DisconnectSession(connID string) (Session, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	session, ok := g.byConn[connID]
	if !ok {
		return Session{}, false
	}
	delete(g.byConn, connID)
	if ids := g.connIDsByUID[session.Identity.UID]; ids != nil {
		delete(ids, connID)
		if len(ids) == 0 {
			delete(g.connIDsByUID, session.Identity.UID)
		}
	}
	return *session, true
}

// Receive 向上游转发一条已认证客户端消息。
func (g *Gateway) Receive(ctx context.Context, connID string, msg rpc.Message) error {
	if g.upstream == nil {
		return ErrUpstreamMissing
	}
	g.mu.RLock()
	session, ok := g.byConn[connID]
	g.mu.RUnlock()
	if !ok {
		return ErrConnectionNotFound
	}
	return g.upstream.Forward(ctx, proxy.Envelope{
		ConnectionID: connID,
		UID:          session.Identity.UID,
		Message:      cloneMessage(msg),
	})
}

// Session 按连接 ID 返回连接级 Session 快照。
func (g *Gateway) Session(connID string) (Session, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	session, ok := g.byConn[connID]
	if !ok {
		return Session{}, false
	}
	return *session, true
}

// ConnectionIDs 返回 uid 当前已认证的连接 ID 列表。
func (g *Gateway) ConnectionIDs(uid int64) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	ids := g.connIDsByUID[uid]
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// SessionsByUID 返回 uid 当前已认证的 Session 快照。
func (g *Gateway) SessionsByUID(uid int64) []Session {
	g.mu.RLock()
	defer g.mu.RUnlock()
	ids := g.connIDsByUID[uid]
	out := make([]Session, 0, len(ids))
	for id := range ids {
		if session := g.byConn[id]; session != nil {
			out = append(out, *session)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ConnectionID < out[j].ConnectionID
	})
	return out
}

func cloneMessage(msg rpc.Message) rpc.Message {
	out := msg
	if len(msg.Payload) > 0 {
		out.Payload = append([]byte(nil), msg.Payload...)
	}
	if len(msg.Header) > 0 {
		out.Header = make(map[string]string, len(msg.Header))
		for key, value := range msg.Header {
			out.Header[key] = value
		}
	}
	return out
}
