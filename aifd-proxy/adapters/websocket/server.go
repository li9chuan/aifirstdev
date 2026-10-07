package websocket

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"

	proxycodec "github.com/li9chuan/aifirstdev/aifd-proxy/codec"
	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	proxyruntime "github.com/li9chuan/aifirstdev/aifd-proxy/runtime"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	coderws "github.com/coder/websocket"
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

// SessionLifecycle 接收 proxy session 断线事件。
type SessionLifecycle interface {
	OnDisconnect(context.Context, proxyruntime.Session) error
}

// SessionLifecycleFunc 将函数适配为 SessionLifecycle。
type SessionLifecycleFunc func(context.Context, proxyruntime.Session) error

// OnDisconnect 调用被包装的断线函数。
func (f SessionLifecycleFunc) OnDisconnect(ctx context.Context, session proxyruntime.Session) error {
	return f(ctx, session)
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
}

// Server 是 websocket client adapter，可直接作为 http.Handler 使用。
type Server struct {
	cfg Config
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
	return &Server{cfg: cfg}
}

// ServeHTTP 接受 websocket 连接并启动 packet 读循环。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := s.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
	if s == nil || s.cfg.Gateway == nil || s.cfg.Delivery == nil || s.cfg.LoginHandler == nil {
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
	if msg.Name != s.cfg.LoginTopic {
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
	if err := s.applyDuplicatePolicy(ctx, conn, result.Identity); err != nil {
		return false, err
	}
	if _, err := s.cfg.Gateway.AcceptIdentity(conn, result.Identity); err != nil {
		_ = conn.CloseWithStatus(coderws.StatusPolicyViolation, err.Error())
		return false, err
	}
	if s.cfg.Downlink != nil {
		if err := s.cfg.Downlink.SubscribeUID(result.Identity.UID); err != nil {
			_, _ = s.cfg.Gateway.DisconnectSession(conn.ID())
			_ = conn.CloseWithStatus(coderws.StatusPolicyViolation, err.Error())
			return false, err
		}
	}
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

func (s *Server) applyDuplicatePolicy(ctx context.Context, conn *Connection, identity proxy.Identity) error {
	existing := s.cfg.Gateway.SessionsByUID(identity.UID)
	if len(existing) == 0 {
		return nil
	}
	decision := DuplicateDecision{Policy: s.cfg.DuplicatePolicy}
	if s.cfg.DuplicateHandler != nil {
		custom, err := s.cfg.DuplicateHandler.ResolveDuplicate(ctx, DuplicateContext{
			NewConnectionID: conn.ID(),
			Identity:        identity,
			Existing:        existing,
		})
		if err != nil {
			_ = conn.CloseWithStatus(coderws.StatusPolicyViolation, err.Error())
			return err
		}
		if custom.Policy != "" {
			decision.Policy = custom.Policy
		}
		decision.Response = custom.Response
		decision.CloseAfterResponse = custom.CloseAfterResponse
	}
	switch decision.Policy {
	case DuplicateAllow:
		return nil
	case DuplicateRejectNew:
		if decision.Response != nil {
			_ = conn.Send(ctx, *decision.Response)
		}
		if decision.CloseAfterResponse || decision.Response == nil {
			_ = conn.Close(ctx)
		}
		return ErrLoginRejected
	case DuplicateKickOld:
		for _, session := range existing {
			removed, ok := s.cfg.Gateway.DisconnectSession(session.ConnectionID)
			if !ok {
				continue
			}
			s.closeSessionAsync(ctx, removed)
		}
		return nil
	default:
		_ = conn.CloseWithStatus(coderws.StatusPolicyViolation, "invalid duplicate policy")
		return fmt.Errorf("invalid duplicate policy %q", decision.Policy)
	}
}

func (s *Server) cleanup(ctx context.Context, connID string) {
	if session, ok := s.cfg.Gateway.DisconnectSession(connID); ok {
		s.closeSession(ctx, session)
		return
	}
	_ = s.cfg.Delivery.Close(ctx, connID)
}

func (s *Server) closeSession(ctx context.Context, session proxyruntime.Session) {
	_ = s.cfg.Delivery.Close(ctx, session.ConnectionID)
	s.afterSessionDisconnect(ctx, session)
}

func (s *Server) closeSessionAsync(ctx context.Context, session proxyruntime.Session) {
	go func(connID string) {
		_ = s.cfg.Delivery.Close(context.Background(), connID)
	}(session.ConnectionID)
	s.afterSessionDisconnect(ctx, session)
}

func (s *Server) afterSessionDisconnect(ctx context.Context, session proxyruntime.Session) {
	uid := session.Identity.UID
	if uid > 0 && len(s.cfg.Gateway.ConnectionIDs(uid)) == 0 && s.cfg.Downlink != nil {
		_ = s.cfg.Downlink.UnsubscribeUID(uid)
	}
	if s.cfg.Lifecycle != nil {
		_ = s.cfg.Lifecycle.OnDisconnect(ctx, session)
	}
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
