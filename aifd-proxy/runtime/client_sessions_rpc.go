package runtime

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	api "github.com/li9chuan/aifirstdev/aifd-rpc"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	proxypb "github.com/li9chuan/aifirstdev/aifd-schema/generated/proxy"
)

const sessionRPCMaxTimeout = 15 * time.Second

// RegisterRPC binds proxy-only lifecycle messages to the UID's business owner.
// These handlers do not load, create, or mark a business User active.
func (m *ClientSessions) RegisterRPC(reg *api.Registry) error {
	if err := api.Server(reg, "OpenClientSessionRequest", func(s *api.Session, req *proxypb.OpenClientSessionRequest) {
		if !trustedProxyRef(s, req.GetSession()) {
			s.Error("FORBIDDEN", "trusted proxy lifecycle metadata required")
			return
		}
		deadline, err := strconv.ParseInt(s.Header(proxy.HeaderSessionDeadline), 10, 64)
		if err != nil || deadline <= m.opts.Now().UnixNano() {
			s.Error("TIMEOUT", "client session activation deadline expired")
			return
		}
		if err := m.Open(s.Context(), s.UID(), req.GetSession()); err != nil {
			RespondSessionError(s, err)
			return
		}
		s.Response(&proxypb.OpenClientSessionResponse{})
	}); err != nil {
		return err
	}
	return api.Server(reg, "ClientSessionClosed", func(s *api.Session, req *proxypb.ClientSessionClosed) {
		if !trustedProxyRef(s, req.GetSession()) {
			return
		}
		_ = m.End(s.Context(), s.UID(), req.GetSession(), req.GetReason())
	})
}

// RegisterHeartbeat provides an optional pure framework heartbeat. Business
// heartbeats use their own PB, validate business conditions, then call RenewCaller.
func (m *ClientSessions) RegisterHeartbeat(reg *api.Registry) error {
	return api.Client(reg, "MceSessionHeartbeat", func(s *api.Session, _ *proxypb.MceSessionHeartbeat) {
		if err := m.RenewCaller(s); err != nil {
			RespondSessionError(s, err)
			return
		}
		s.Response(&proxypb.MseSessionHeartbeat{ServerTimeUnixMs: m.opts.Now().UnixMilli()})
	})
}

// ValidateCaller verifies the current invocation's exact connection without renewing it.
func (m *ClientSessions) ValidateCaller(s *api.Session) error {
	ref, err := proxy.ClientSessionRef(s)
	if err != nil {
		return err
	}
	return m.Validate(s.Context(), s.UID(), ref)
}

// RenewCaller renews only the trusted connection associated with this invocation.
func (m *ClientSessions) RenewCaller(s *api.Session) error {
	ref, err := proxy.ClientSessionRef(s)
	if err != nil {
		return err
	}
	return m.Renew(s.Context(), s.UID(), ref)
}

// RespondSessionError completes the original RPC with a stable liveness error.
func RespondSessionError(s *api.Session, err error) {
	code := "SESSION_NOT_BOUND"
	if errors.Is(err, ErrSessionExpired) {
		code = "SESSION_EXPIRED"
	}
	if errors.Is(err, ErrSessionClosed) {
		code = "SESSION_CLOSED"
	}
	if errors.Is(err, ErrInvalidIdentity) || errors.Is(err, proxy.ErrClientSessionMissing) {
		code = "FORBIDDEN"
	}
	s.Error(code, err.Error())
}

func trustedProxyRef(s *api.Session, ref *proxypb.ClientSessionRef) bool {
	return s.UID() > 0 && proxy.ValidSessionRef(ref) && s.Header(proxy.HeaderCallerKind) == proxy.CallerProxy &&
		s.Header(proxy.HeaderProxyInstanceID) == ref.GetProxyInstanceId() && s.Header(proxy.HeaderConnectionID) == ref.GetConnectionId()
}

// Ref returns the native connection's immutable wire reference.
func (s Session) Ref() *proxypb.ClientSessionRef {
	return &proxypb.ClientSessionRef{ProxyInstanceId: s.ProxyInstanceID, ConnectionId: s.ConnectionID}
}

// RPCSessionLifecycle binds a live authenticated proxy to the business owner's
// lifecycle routes. Local and NATS buses use the same messages and metadata.
type RPCSessionLifecycle struct {
	Bus     rpc.Bus
	Timeout time.Duration
}

// NewRPCSessionLifecycle creates the standard proxy lifecycle adapter.
func NewRPCSessionLifecycle(bus rpc.Bus) *RPCSessionLifecycle {
	return &RPCSessionLifecycle{Bus: bus, Timeout: sessionRPCMaxTimeout}
}

// OnConnect waits for the business owner to accept this exact connection lease.
func (l *RPCSessionLifecycle) OnConnect(ctx context.Context, session Session) error {
	if l == nil || l.Bus == nil {
		return ErrUpstreamMissing
	}
	msg, err := rpc.NewMessagePB("", &proxypb.OpenClientSessionRequest{Session: session.Ref()})
	if err != nil {
		return err
	}
	decorateProxyLifecycle(&msg, session)
	timeout := l.Timeout
	if timeout <= 0 || timeout > sessionRPCMaxTimeout {
		timeout = sessionRPCMaxTimeout
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	deadline, _ := requestCtx.Deadline()
	msg.Header[proxy.HeaderSessionDeadline] = strconv.FormatInt(deadline.UnixNano(), 10)
	rsp, err := l.Bus.Request(requestCtx, session.Identity.UID, msg, rpc.RequestOptions{Timeout: timeout})
	if err != nil {
		return err
	}
	if err := rpc.StatusFromMessage(rsp); err != nil {
		return err
	}
	if rsp.Name != "OpenClientSessionResponse" {
		return fmt.Errorf("unexpected client session response %q", rsp.Name)
	}
	return rsp.DecodePB(&proxypb.OpenClientSessionResponse{})
}

// OnDisconnect notifies the owner; expiry is the fallback when delivery fails.
func (l *RPCSessionLifecycle) OnDisconnect(ctx context.Context, session Session, reason proxypb.SessionEndReason) error {
	if l == nil || l.Bus == nil {
		return ErrUpstreamMissing
	}
	msg, err := rpc.NewMessagePB("", &proxypb.ClientSessionClosed{Session: session.Ref(), Reason: reason})
	if err != nil {
		return err
	}
	decorateProxyLifecycle(&msg, session)
	notifyCtx, cancel := context.WithTimeout(ctx, l.Timeout)
	defer cancel()
	return l.Bus.Tell(notifyCtx, session.Identity.UID, msg)
}

func decorateProxyLifecycle(msg *rpc.Message, session Session) {
	if msg.Header == nil {
		msg.Header = make(map[string]string)
	}
	msg.Header[proxy.HeaderCallerKind] = proxy.CallerProxy
	msg.Header[proxy.HeaderProxyInstanceID] = session.ProxyInstanceID
	msg.Header[proxy.HeaderConnectionID] = session.ConnectionID
}
