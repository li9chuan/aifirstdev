package websocket

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	log "github.com/li9chuan/aifirstdev/aifd-observe/logging"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	coderws "github.com/coder/websocket"
	proxycodec "github.com/li9chuan/aifirstdev/aifd-proxy/codec"
	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	proxyruntime "github.com/li9chuan/aifirstdev/aifd-proxy/runtime"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	proxypb "github.com/li9chuan/aifirstdev/aifd-schema/generated/proxy"
)

const (
	defaultMaxMessageBytes = 1024 * 1024
	defaultLoginTopic      = "MceLogin"
	defaultPingTopic       = "McePing"
	defaultPongTopic       = "MsePong"
)

var (
	// ErrConfigMissing 表示 websocket server 缺少必要依赖。
	ErrConfigMissing = errors.New("proxy websocket config is missing required dependency")
	// ErrLoginRejected 表示重复登录策略拒绝了新连接。
	ErrLoginRejected = errors.New("proxy websocket login rejected")
)

// DuplicatePolicy 定义同 UID 重复登录时的默认处理策略。
type DuplicatePolicy string

const (
	// DuplicateAllow 允许同一 UID 多连接在线。
	DuplicateAllow DuplicatePolicy = "allow"
	// DuplicateKickOld 踢掉旧连接，保留新连接。
	DuplicateKickOld DuplicatePolicy = "kick_old"
	// DuplicateRejectNew 保留旧连接，拒绝新连接。
	DuplicateRejectNew DuplicatePolicy = "reject_new"
)

// DuplicateContext 描述一次重复登录判定。
type DuplicateContext struct {
	NewConnectionID string
	Identity        proxy.Identity
	Existing        []proxyruntime.Session
}

// DuplicateDecision 是自定义重复登录处理器的返回值。
type DuplicateDecision struct {
	Policy             DuplicatePolicy
	Response           *rpc.Message
	CloseAfterResponse bool
}

// DuplicateHandler 允许业务覆盖默认重复登录策略。
type DuplicateHandler interface {
	ResolveDuplicate(context.Context, DuplicateContext) (DuplicateDecision, error)
}

// DuplicateHandlerFunc 将函数适配为 DuplicateHandler。
type DuplicateHandlerFunc func(context.Context, DuplicateContext) (DuplicateDecision, error)

// ResolveDuplicate 调用被包装的重复登录处理函数。
func (f DuplicateHandlerFunc) ResolveDuplicate(ctx context.Context, req DuplicateContext) (DuplicateDecision, error) {
	return f(ctx, req)
}

// SessionLifecycle owns authenticated connection activation and shutdown notices.
type SessionLifecycle interface {
	OnConnect(context.Context, proxyruntime.Session) error
	OnDisconnect(context.Context, proxyruntime.Session, proxypb.SessionEndReason) error
}

// SessionLifecycleFuncs adapts optional application lifecycle hooks.
type SessionLifecycleFuncs struct {
	Connected    func(context.Context, proxyruntime.Session) error
	Disconnected func(context.Context, proxyruntime.Session, proxypb.SessionEndReason) error
}

func (f SessionLifecycleFuncs) OnConnect(ctx context.Context, session proxyruntime.Session) error {
	if f.Connected == nil {
		return nil
	}
	return f.Connected(ctx, session)
}

func (f SessionLifecycleFuncs) OnDisconnect(ctx context.Context, session proxyruntime.Session, reason proxypb.SessionEndReason) error {
	if f.Disconnected == nil {
		return nil
	}
	return f.Disconnected(ctx, session, reason)
}

// Config 配置 websocket 客户端入口。
type Config struct {
	Addr            string
	MaxMessageBytes int64
	LoginTopic      string
	PingTopic       string
	PongTopic       string
	PublicTopics    []string

	DuplicatePolicy  DuplicatePolicy
	Gateway          *proxyruntime.Gateway
	Delivery         *proxyruntime.Delivery
	LoginHandler     proxy.LoginHandler
	PublicHandler    proxy.PublicHandler
	Downlink         proxy.DownlinkSubscriber
	DuplicateHandler DuplicateHandler
	Lifecycle        SessionLifecycle
	HandshakeHandler proxy.HandshakeHandler
	Bus              rpc.Bus
}

// Server 是 websocket client adapter，可直接作为 http.Handler 使用。
type Server struct {
	cfg          Config
	membershipMu sync.Locker
	closed       atomic.Bool
}

var connectionSeq atomic.Uint64

type loginTopicProvider interface {
	Topic() string
}

type loginHandlerValidator interface {
	Validate() error
}

// NewServer 创建 websocket client adapter。
func NewServer(cfg Config) *Server {
	if cfg.Delivery == nil {
		cfg.Delivery = proxyruntime.NewDelivery()
	}
	if cfg.Gateway == nil && cfg.Bus != nil {
		cfg.Gateway = proxyruntime.NewGateway(nil, proxyruntime.NewRPCUpstream(cfg.Bus, cfg.Delivery))
	}
	if cfg.Lifecycle == nil && cfg.Bus != nil {
		cfg.Lifecycle = proxyruntime.NewRPCSessionLifecycle(cfg.Bus)
	}
	if cfg.MaxMessageBytes <= 0 {
		cfg.MaxMessageBytes = defaultMaxMessageBytes
	}
	if cfg.LoginTopic == "" {
		cfg.LoginTopic = loginHandlerTopic(cfg.LoginHandler)
	}
	if cfg.LoginTopic == "" {
		cfg.LoginTopic = defaultLoginTopic
	}
	if cfg.PingTopic == "" {
		cfg.PingTopic = defaultPingTopic
	}
	if cfg.PongTopic == "" {
		cfg.PongTopic = defaultPongTopic
	}
	if cfg.DuplicatePolicy == "" {
		cfg.DuplicatePolicy = DuplicateKickOld
	}
	var membership sync.Locker = &sync.Mutex{}
	if cfg.Gateway != nil {
		membership = cfg.Gateway.MembershipLock()
	}
	return &Server{cfg: cfg, membershipMu: membership}
}

// ServeHTTP 接受 websocket 连接并启动 packet 读循环。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s != nil && s.closed.Load() {
		http.Error(w, "proxy is closed", http.StatusServiceUnavailable)
		return
	}
	if err := s.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var handshake proxy.HandshakeResult
	if s.cfg.HandshakeHandler != nil {
		var err error
		handshake, err = s.cfg.HandshakeHandler.AuthenticateHandshake(r.Context(), proxy.AuthRequest{Token: r.URL.Query().Get("token"), RemoteAddr: r.RemoteAddr, Header: headerSnapshot(r.Header)})
		if err != nil || handshake.Handled && handshake.Identity.UID <= 0 {
			http.Error(w, "authentication failed", http.StatusUnauthorized)
			return
		}
	}
	if !handshake.Handled && s.cfg.LoginHandler == nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	ws, err := coderws.Accept(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(s.cfg.MaxMessageBytes)
	conn := newConnection(s.nextConnectionID(), ws)
	if err := s.cfg.Delivery.Add(conn); err != nil {
		_ = conn.Close(r.Context())
		return
	}
	defer s.cleanup(r.Context(), conn.ID())
	if handshake.Handled {
		if err := s.activateConnection(r.Context(), conn, handshake.Identity); err != nil {
			return
		}
		if handshake.InitialMessage != nil {
			initial := handshake.InitialMessage.Clone()
			delete(initial.Header, rpc.HeaderClientInbox)
			if err := conn.Send(r.Context(), initial); err != nil {
				return
			}
		}
	}
	s.serveConn(r.Context(), conn, r)
}

// ListenAndServe 启动 http server，并在 ctx 取消时 shutdown。
func (s *Server) ListenAndServe(ctx context.Context) error {
	if err := s.Validate(); err != nil {
		return err
	}
	srv := &http.Server{Addr: s.cfg.Addr, Handler: s}
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		_ = s.Close(context.Background())
		shutdownErr := srv.Shutdown(context.Background())
		if shutdownErr != nil {
			return shutdownErr
		}
		return ctx.Err()
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Validate 检查 websocket server 的依赖、登录 topic 和 handler 配置。
func (s *Server) Validate() error {
	if s == nil || s.cfg.Gateway == nil || s.cfg.Delivery == nil || s.cfg.LoginHandler == nil && s.cfg.HandshakeHandler == nil {
		return ErrConfigMissing
	}
	if validator, ok := s.cfg.LoginHandler.(loginHandlerValidator); ok {
		if err := validator.Validate(); err != nil {
			return err
		}
	}
	if handlerTopic := loginHandlerTopic(s.cfg.LoginHandler); handlerTopic != "" && s.cfg.LoginTopic != handlerTopic {
		return fmt.Errorf("%w: login topic %q does not match handler topic %q", ErrConfigMissing, s.cfg.LoginTopic, handlerTopic)
	}
	if err := proxycodec.ValidateClientTopic(s.cfg.LoginTopic); err != nil {
		return err
	}
	if s.cfg.PingTopic != "" {
		if err := proxycodec.ValidateClientTopic(s.cfg.PingTopic); err != nil {
			return err
		}
	}
	if len(s.cfg.PublicTopics) > 0 && s.cfg.PublicHandler == nil {
		return ErrConfigMissing
	}
	for _, topic := range s.cfg.PublicTopics {
		if err := proxycodec.ValidateClientTopic(topic); err != nil {
			return err
		}
	}
	return nil
}

func loginHandlerTopic(handler proxy.LoginHandler) string {
	if provider, ok := handler.(loginTopicProvider); ok {
		return provider.Topic()
	}
	return ""
}

func (s *Server) serveConn(ctx context.Context, conn *Connection, req *http.Request) {
	defer s.cleanup(ctx, conn.ID())
	for {
		msgType, data, err := conn.ws.Read(ctx)
		if err != nil {
			return
		}
		if msgType != coderws.MessageBinary {
			_ = conn.CloseWithStatus(coderws.StatusUnsupportedData, "binary frame required")
			return
		}
		msg, err := decodeMessage(data)
		if err != nil {
			_ = conn.CloseWithStatus(coderws.StatusPolicyViolation, "invalid packet")
			return
		}
		if err := proxycodec.ValidateClientTopic(msg.Name); err != nil {
			_ = conn.CloseWithStatus(coderws.StatusPolicyViolation, "invalid client topic")
			return
		}
		if _, ok := s.cfg.Gateway.Session(conn.ID()); !ok {
			if s.isPublicTopic(msg.Name) {
				keep, err := s.handlePublic(ctx, conn, req, msg)
				if err != nil || !keep {
					return
				}
				continue
			}
			keep, err := s.handleLogin(ctx, conn, req, msg)
			if err != nil || !keep {
				return
			}
			continue
		}
		if s.isPing(msg.Name) {
			_ = conn.Send(ctx, rpc.Message{Name: s.cfg.PongTopic})
			continue
		}
		if err := s.cfg.Gateway.Receive(ctx, conn.ID(), msg); err != nil {
			_ = conn.CloseWithStatus(coderws.StatusPolicyViolation, err.Error())
			return
		}
	}
}

func (s *Server) handleLogin(ctx context.Context, conn *Connection, req *http.Request, msg rpc.Message) (bool, error) {
	if msg.Name != s.cfg.LoginTopic || s.cfg.LoginHandler == nil {
		_ = conn.CloseWithStatus(coderws.StatusPolicyViolation, "login required")
		return false, ErrLoginRejected
	}
	result, err := s.cfg.LoginHandler.Login(ctx, proxy.LoginRequest{
		ConnectionID: conn.ID(),
		RemoteAddr:   req.RemoteAddr,
		Header:       headerSnapshot(req.Header),
		Message:      msg,
	})
	if err != nil {
		_ = conn.CloseWithStatus(coderws.StatusPolicyViolation, err.Error())
		return false, err
	}
	if result.Identity.UID <= 0 {
		if result.Response != nil {
			preserveClientInbox(msg, result.Response)
			_ = conn.Send(ctx, *result.Response)
		}
		if result.CloseAfterResponse {
			_ = conn.Close(ctx)
			return false, nil
		}
		return true, nil
	}
	if err := s.activateConnection(ctx, conn, result.Identity); err != nil {
		return false, err
	}
	if result.Response != nil {
		preserveClientInbox(msg, result.Response)
		if err := conn.Send(ctx, *result.Response); err != nil {
			return false, err
		}
	}
	if result.CloseAfterResponse {
		_ = conn.Close(ctx)
		return false, nil
	}
	return true, nil
}

func (s *Server) handlePublic(ctx context.Context, conn *Connection, req *http.Request, msg rpc.Message) (bool, error) {
	if s.cfg.PublicHandler == nil {
		_ = conn.CloseWithStatus(coderws.StatusPolicyViolation, "public handler missing")
		return false, ErrConfigMissing
	}
	result, err := s.cfg.PublicHandler.HandlePublic(ctx, proxy.PublicRequest{
		ConnectionID: conn.ID(),
		RemoteAddr:   req.RemoteAddr,
		Header:       headerSnapshot(req.Header),
		Message:      msg,
	})
	if err != nil {
		_ = conn.CloseWithStatus(coderws.StatusPolicyViolation, err.Error())
		return false, err
	}
	if result.Response != nil {
		preserveClientInbox(msg, result.Response)
		_ = conn.Send(ctx, *result.Response)
	}
	return true, nil
}

func (s *Server) duplicateDecision(ctx context.Context, conn *Connection, identity proxy.Identity) (DuplicateDecision, error) {
	decision := DuplicateDecision{Policy: s.cfg.DuplicatePolicy}
	existing := s.cfg.Gateway.SessionsByUID(identity.UID)
	if len(existing) > 0 && s.cfg.DuplicateHandler != nil {
		custom, err := s.cfg.DuplicateHandler.ResolveDuplicate(ctx, DuplicateContext{NewConnectionID: conn.ID(), Identity: identity, Existing: existing})
		if err != nil {
			return decision, err
		}
		if custom.Policy != "" {
			decision.Policy = custom.Policy
		}
		decision.Response, decision.CloseAfterResponse = custom.Response, custom.CloseAfterResponse
	}
	return decision, nil
}

// activateConnection is shared by HTTP handshake and packet login. Membership and
// first/last downlink subscription changes are serialized; business hooks and socket
// writes run after releasing the membership lock.
func (s *Server) activateConnection(ctx context.Context, conn *Connection, identity proxy.Identity) error {
	decision, err := s.duplicateDecision(ctx, conn, identity)
	if err != nil {
		return err
	}
	s.membershipMu.Lock()
	if s.closed.Load() {
		s.membershipMu.Unlock()
		return ErrConfigMissing
	}
	existing := s.cfg.Gateway.SessionsByUID(identity.UID)
	var removed []proxyruntime.Session
	switch decision.Policy {
	case DuplicateAllow:
	case DuplicateRejectNew:
		if len(existing) > 0 {
			err = ErrLoginRejected
		}
	case DuplicateKickOld:
		for _, old := range existing {
			if current, ok := s.cfg.Gateway.DisconnectSession(old.ConnectionID); ok {
				removed = append(removed, current)
			}
		}
	default:
		err = fmt.Errorf("invalid duplicate policy %q", decision.Policy)
	}
	var session proxyruntime.Session
	if err == nil {
		session, err = s.cfg.Gateway.AcceptIdentity(conn, identity)
		if err == nil && s.cfg.Downlink != nil && len(existing) == 0 {
			err = s.cfg.Downlink.SubscribeUID(identity.UID)
			if err != nil {
				_, _ = s.cfg.Gateway.DisconnectSession(conn.ID())
				if len(s.cfg.Gateway.ConnectionIDs(identity.UID)) == 0 {
					_ = s.cfg.Downlink.UnsubscribeUID(identity.UID)
				}
			}
		}
	}
	s.membershipMu.Unlock()
	for _, old := range removed {
		s.closeSessionAsync(ctx, old, proxypb.SessionEndReason_SESSION_END_REASON_REPLACED)
	}
	if err != nil {
		if decision.Response != nil {
			_ = conn.Send(ctx, *decision.Response)
		}
		s.cleanup(ctx, conn.ID())
		_ = conn.CloseWithStatus(coderws.StatusPolicyViolation, err.Error())
		return err
	}
	if s.cfg.Lifecycle != nil {
		if err := s.cfg.Lifecycle.OnConnect(ctx, session); err != nil {
			s.cleanup(ctx, conn.ID())
			_ = conn.CloseWithStatus(coderws.StatusPolicyViolation, err.Error())
			return err
		}
	}
	if _, ok := s.cfg.Gateway.Session(conn.ID()); s.closed.Load() || !ok {
		s.cleanup(ctx, conn.ID())
		return ErrLoginRejected
	}
	return nil
}

func (s *Server) cleanup(ctx context.Context, connID string) {
	s.membershipMu.Lock()
	session, ok := s.cfg.Gateway.DisconnectSession(connID)
	s.membershipMu.Unlock()
	if ok {
		s.closeSession(ctx, session, proxypb.SessionEndReason_SESSION_END_REASON_DISCONNECTED)
		return
	}
	_ = s.cfg.Delivery.Close(ctx, connID)
}

func (s *Server) closeSession(ctx context.Context, session proxyruntime.Session, reason proxypb.SessionEndReason) {
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_ = s.cfg.Delivery.Close(closeCtx, session.ConnectionID)
	s.afterSessionDisconnect(closeCtx, session, reason)
}

func (s *Server) closeSessionAsync(ctx context.Context, session proxyruntime.Session, reason proxypb.SessionEndReason) {
	go func(connID string) {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.cfg.Delivery.Close(closeCtx, connID)
	}(session.ConnectionID)
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	s.afterSessionDisconnect(closeCtx, session, reason)
}

func (s *Server) afterSessionDisconnect(ctx context.Context, session proxyruntime.Session, reason proxypb.SessionEndReason) {
	uid := session.Identity.UID
	s.membershipMu.Lock()
	if uid > 0 && len(s.cfg.Gateway.ConnectionIDs(uid)) == 0 && s.cfg.Downlink != nil {
		_ = s.cfg.Downlink.UnsubscribeUID(uid)
	}
	s.membershipMu.Unlock()
	if s.cfg.Lifecycle != nil {
		if err := s.cfg.Lifecycle.OnDisconnect(ctx, session, reason); err != nil {
			log.Warnf("proxy close notice UID=%d connection=%s reason=%s: %v", uid, session.ConnectionID, reason.String(), err)
		}
	}
}

// Close closes authenticated sockets and emits per-connection shutdown notices.
func (s *Server) Close(ctx context.Context) error {
	s.closed.Store(true)
	for _, current := range s.cfg.Gateway.Sessions() {
		s.membershipMu.Lock()
		removed, ok := s.cfg.Gateway.DisconnectSession(current.ConnectionID)
		s.membershipMu.Unlock()
		if ok {
			s.closeSession(ctx, removed, proxypb.SessionEndReason_SESSION_END_REASON_SHUTDOWN)
		}
	}
	return s.cfg.Delivery.CloseAll(ctx)
}

func (s *Server) isPing(topic string) bool {
	return s.cfg.PingTopic != "" && topic == s.cfg.PingTopic
}

func (s *Server) isPublicTopic(topic string) bool {
	for _, publicTopic := range s.cfg.PublicTopics {
		if topic == publicTopic {
			return true
		}
	}
	return false
}

func (s *Server) nextConnectionID() string {
	return "ws-" + strconv.FormatUint(connectionSeq.Add(1), 10)
}

func preserveClientInbox(req rpc.Message, rsp *rpc.Message) {
	if rsp == nil || req.Header == nil || req.Header[rpc.HeaderClientInbox] == "" {
		return
	}
	if rsp.Header == nil {
		rsp.Header = make(map[string]string, 1)
	}
	if rsp.Header[rpc.HeaderClientInbox] == "" {
		rsp.Header[rpc.HeaderClientInbox] = req.Header[rpc.HeaderClientInbox]
	}
}

func decodeMessage(data []byte) (rpc.Message, error) {
	head, body, err := proxycodec.DecodePacket(data)
	if err != nil {
		return rpc.Message{}, err
	}
	return proxycodec.MessageFromPacket(head, body)
}

func headerSnapshot(header http.Header) map[string]string {
	out := make(map[string]string, len(header))
	for key, values := range header {
		if len(values) > 0 {
			out[key] = values[0]
		}
	}
	return out
}

// Connection 是 websocket 传输连接。
type Connection struct {
	id     string
	ws     *coderws.Conn
	mu     sync.Mutex
	closed atomic.Bool
}

func newConnection(id string, ws *coderws.Conn) *Connection {
	return &Connection{id: id, ws: ws}
}

// ID 返回 proxy 连接 ID。
func (c *Connection) ID() string {
	return c.id
}

// Send 将 rpc.Message 编码为 proxy packet 并写入 binary frame。
func (c *Connection) Send(ctx context.Context, msg rpc.Message) error {
	parts, err := proxycodec.EncodeMessage(msg)
	if err != nil {
		return err
	}
	data := bytes.Join(parts, nil)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		return coderws.CloseError{Code: coderws.StatusNormalClosure, Reason: "connection closed"}
	}
	return c.ws.Write(ctx, coderws.MessageBinary, data)
}

// Close 关闭 websocket 连接。
func (c *Connection) Close(context.Context) error {
	return c.CloseWithStatus(coderws.StatusNormalClosure, "closed")
}

// CloseWithStatus 使用指定 websocket 状态关闭连接。
func (c *Connection) CloseWithStatus(code coderws.StatusCode, reason string) error {
	if c.closed.Swap(true) {
		return nil
	}
	return c.ws.Close(code, reason)
}
