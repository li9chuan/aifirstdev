package nats

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	natsgo "github.com/nats-io/nats.go"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	rpcruntime "github.com/li9chuan/aifirstdev/aifd-rpc/runtime"
	"google.golang.org/protobuf/proto"
)

// ErrBusClosed 表示使用了已关闭的 NATS bus。
var ErrBusClosed = errors.New("nats rpc bus closed")

// Config 控制 NATS 连接选项、subject 前缀和本地缓冲。
type Config struct {
	Options             natsgo.Options
	Buffer              int
	LogDebug            bool
	DefaultTimeout      time.Duration
	StatsWindow         time.Duration
	SubjectPrefix       string
	ClientSubjectPrefix string
	StreamSubjectPrefix string
}

// Bus 基于 NATS request/reply subject 和 JetStream 事件实现 rpc.Bus。
type Bus struct {
	cfg        Config
	nc         *natsgo.Conn
	js         natsgo.JetStreamContext
	router     *rpcruntime.Router
	deliveries chan rpc.Delivery
	callbacks  *rpcruntime.CallbackCache
	stats      *rpcruntime.StatsCollector
	closing    atomic.Bool
	closed     atomic.Bool
	runner     atomic.Bool
	closeOnce  sync.Once
	deliveryWG sync.WaitGroup

	subsMu sync.Mutex
	subs   []*natsgo.Subscription
}

// NewBus 连接 NATS，并初始化 RPC routing、callback 和 stats。
func NewBus(cfg Config) (*Bus, error) {
	applyDefaults(&cfg)
	nc, err := cfg.Options.Connect()
	if err != nil {
		return nil, err
	}
	js, jsErr := nc.JetStream()
	if jsErr != nil {
		nc.Close()
		return nil, jsErr
	}
	serviceName := cfg.Options.Name
	b := &Bus{
		cfg:        cfg,
		nc:         nc,
		js:         js,
		router:     rpcruntime.NewRouter(),
		deliveries: make(chan rpc.Delivery, cfg.Buffer),
		callbacks:  rpcruntime.NewCallbackCache(cfg.DefaultTimeout),
		stats:      rpcruntime.NewStatsCollector(serviceName, cfg.StatsWindow),
	}
	return b, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Buffer <= 0 {
		cfg.Buffer = 1024 * 100
	}
	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = 15 * time.Second
	}
	if cfg.SubjectPrefix == "" {
		cfg.SubjectPrefix = "RPC."
	}
	if cfg.ClientSubjectPrefix == "" {
		cfg.ClientSubjectPrefix = "S."
	}
	if cfg.StreamSubjectPrefix == "" {
		cfg.StreamSubjectPrefix = "js."
	}
}

// Register 为单个 route 订阅直接 NATS subject。
func (b *Bus) Register(route rpc.Route) error {
	if err := b.router.Register(route); err != nil {
		return err
	}
	sub, err := b.nc.Subscribe(b.subject(route.Name), func(msg *natsgo.Msg) {
		b.handleRouteMessage(route, msg)
	})
	if err != nil {
		return err
	}
	b.addSub(sub)
	return nil
}

// RegisterQueue 为单个 route 订阅 queue group。
func (b *Bus) RegisterQueue(route rpc.Route, queue string) error {
	route.Queue = queue
	if err := b.router.RegisterQueue(route, queue); err != nil {
		return err
	}
	sub, err := b.nc.QueueSubscribe(b.subject(route.Name), queue, func(msg *natsgo.Msg) {
		b.handleRouteMessage(route, msg)
	})
	if err != nil {
		return err
	}
	b.addSub(sub)
	return nil
}

// RegisterStream 为单个 route 订阅 durable JetStream consumer。
func (b *Bus) RegisterStream(_ context.Context, route rpc.Route) error {
	route.Kind = rpc.RouteKindStream
	if err := b.router.RegisterStream(route); err != nil {
		return err
	}
	consumer := strings.ReplaceAll(route.Name, ".", "_")
	sub, err := b.js.Subscribe(b.streamSubject(route.Name), func(msg *natsgo.Msg) {
		b.handleRouteMessage(route, msg)
	}, natsgo.Durable(consumer), natsgo.DeliverNew())
	if err != nil {
		return err
	}
	b.addSub(sub)
	return nil
}

// Tell 向 route subject 发布单向请求消息。
func (b *Bus) Tell(ctx context.Context, uid int64, msg rpc.Message) error {
	return b.publishRequest(ctx, uid, 0, msg, "")
}

// TellPB marshal protobuf 请求，并通过 Tell 发送。
func (b *Bus) TellPB(ctx context.Context, uid int64, name string, pb proto.Message) error {
	msg, err := rpc.NewMessagePB(name, pb)
	if err != nil {
		return err
	}
	return b.Tell(ctx, uid, msg)
}

// Call 发布请求，并在提供 cb 时存储一次性 reply callback。
func (b *Bus) Call(ctx context.Context, uid int64, msg rpc.Message, cb rpc.Handler, opts rpc.CallOptions) error {
	if cb == nil {
		return b.Tell(ctx, uid, msg)
	}
	inbox := b.nc.NewRespInbox()
	var sub *natsgo.Subscription
	sub, err := b.nc.Subscribe(inbox, func(nmsg *natsgo.Msg) {
		rsp := messageFromNATS(nmsg, msg.Name)
		session := rpcruntime.NewSession(rpcruntime.SessionOptions{UID: uid, Topic: rsp.Name, Inbox: inbox})
		ok, _ := b.callbacks.Resolve(context.Background(), inbox, session, rsp)
		if ok {
			b.stats.RecordRspIn(msg.Name, len(rsp.Payload))
		}
	})
	if err != nil {
		return err
	}
	_ = sub.AutoUnsubscribe(1)
	if err := b.callbacks.Add(rpcruntime.CallbackEntry{
		Inbox:     inbox,
		RouteName: msg.Name,
		UID:       uid,
		Handler:   cb,
		OnTimeout: opts.OnTimeout,
		Cleanup: func() {
			_ = sub.Unsubscribe()
		},
		Timeout: opts.Timeout,
	}); err != nil {
		_ = sub.Unsubscribe()
		return err
	}
	if err := b.publishRequest(ctx, uid, 0, msg, inbox); err != nil {
		b.callbacks.Cancel(inbox)
		return err
	}
	return nil
}

// CallPB marshal protobuf 请求，并通过 Call 发送。
func (b *Bus) CallPB(ctx context.Context, uid int64, name string, pb proto.Message, cb rpc.Handler, opts rpc.CallOptions) error {
	msg, err := rpc.NewMessagePB(name, pb)
	if err != nil {
		return err
	}
	return b.Call(ctx, uid, msg, cb, opts)
}

// Request 发布请求，并等待一个响应或超时。
func (b *Bus) Request(ctx context.Context, uid int64, msg rpc.Message, opts rpc.RequestOptions) (rpc.Message, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = b.cfg.DefaultTimeout
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	rspCh := make(chan rpc.Message, 1)
	errCh := make(chan error, 1)
	err := b.Call(reqCtx, uid, msg, func(_ context.Context, _ rpc.Session, rsp rpc.Message) error {
		rspCh <- rsp.Clone()
		return nil
	}, rpc.CallOptions{
		Timeout: timeout,
		OnTimeout: func(context.Context) error {
			errCh <- context.DeadlineExceeded
			return nil
		},
	})
	if err != nil {
		return rpc.Message{}, err
	}
	select {
	case <-reqCtx.Done():
		return rpc.Message{}, reqCtx.Err()
	case err := <-errCh:
		return rpc.Message{}, err
	case rsp := <-rspCh:
		return rsp, nil
	}
}

// RequestPB marshal protobuf 请求，并解码 protobuf 响应。
func (b *Bus) RequestPB(ctx context.Context, uid int64, name string, req proto.Message, rsp proto.Message, opts rpc.RequestOptions) error {
	msg, err := rpc.NewMessagePB(name, req)
	if err != nil {
		return err
	}
	response, err := b.Request(ctx, uid, msg, opts)
	if err != nil {
		return err
	}
	if err := rpc.StatusFromMessage(response); err != nil {
		return err
	}
	return response.DecodePB(rsp)
}

// Push 向 uid 对应的客户端 subject 发布下行消息。
func (b *Bus) Push(ctx context.Context, uid int64, msg rpc.Message) error {
	if b.closed.Load() {
		return ErrBusClosed
	}
	nmsg := natsgo.NewMsg(b.clientSubject(uid))
	applyMessageToNATS(nmsg, msg)
	nmsg.Header.Del(rpc.HeaderClientInbox)
	if nmsg.Header.Get(rpc.HeaderResponse) == "" {
		nmsg.Header.Set(rpc.HeaderResponse, msg.Name)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return b.nc.PublishMsg(nmsg)
}

// PushPB marshal protobuf push 消息，并通过 Push 发布。
func (b *Bus) PushPB(ctx context.Context, uid int64, pb proto.Message) error {
	msg, err := rpc.NewMessagePB("", pb)
	if err != nil {
		return err
	}
	return b.Push(ctx, uid, msg)
}

// SaveToStream 将消息发布到配置的 JetStream subject。
func (b *Bus) SaveToStream(ctx context.Context, uid int64, msg rpc.Message) error {
	if b.closed.Load() {
		return ErrBusClosed
	}
	nmsg := natsgo.NewMsg(b.streamSubject(msg.Name))
	applyMessageToNATS(nmsg, msg)
	nmsg.Header.Set(rpc.HeaderUID, strconv.FormatInt(uid, 10))
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	_, err := b.js.PublishMsg(nmsg)
	if err == nil {
		b.stats.RecordReqOut(msg.Name, len(msg.Payload))
	}
	return err
}

// SavePBToStream marshal protobuf 事件，并通过 SaveToStream 发布。
func (b *Bus) SavePBToStream(ctx context.Context, uid int64, name string, pb proto.Message) error {
	msg, err := rpc.NewMessagePB(name, pb)
	if err != nil {
		return err
	}
	return b.SaveToStream(ctx, uid, msg)
}

// Deliveries 向 runtime runner 暴露 route handler delivery。
func (b *Bus) Deliveries() <-chan rpc.Delivery {
	b.runner.Store(true)
	return b.deliveries
}

// Stats 返回 NATS bus stats collector。
func (b *Bus) Stats() rpc.Stats {
	return b.stats
}

// Close drain route 订阅，等待已入队 handler 完成，再 drain NATS 连接。
func (b *Bus) Close(ctx context.Context) error {
	var err error
	b.closeOnce.Do(func() {
		closeCtx, cancel := b.closeContext(ctx)
		defer cancel()
		b.closing.Store(true)
		err = errors.Join(err, b.drainSubscriptions(closeCtx))
		close(b.deliveries)
		if b.runner.Load() {
			err = errors.Join(err, waitDeliveries(closeCtx, &b.deliveryWG))
		}
		b.callbacks.Close()
		if b.nc != nil {
			if drainErr := b.nc.Drain(); drainErr != nil {
				err = errors.Join(err, drainErr)
			}
			err = errors.Join(err, waitNATSClosed(closeCtx, b.nc))
		}
		b.closed.Store(true)
	})
	return err
}

// NatsConn 返回底层 NATS connection，供集成 hook 使用。
func (b *Bus) NatsConn() *natsgo.Conn {
	return b.nc
}

func (b *Bus) handleRouteMessage(route rpc.Route, nmsg *natsgo.Msg) {
	if b.closed.Load() {
		return
	}
	if status := nmsg.Header.Get(rpc.HeaderStatus); status != "" {
		return
	}
	msg := messageFromNATS(nmsg, route.Name)
	uid := parseHeaderInt64(msg.Header, rpc.HeaderUID)
	clientInbox := int(parseHeaderInt64(msg.Header, rpc.HeaderClientInbox))
	b.stats.RecordReqIn(route.Name, len(msg.Payload))
	session := rpcruntime.NewSession(rpcruntime.SessionOptions{
		UID:         uid,
		Topic:       route.Name,
		Inbox:       nmsg.Reply,
		ClientInbox: clientInbox,
		Respond: func(ctx context.Context, rsp rpc.Message) error {
			b.stats.RecordRspOut(route.Name, len(rsp.Payload))
			if nmsg.Reply != "" {
				out := natsgo.NewMsg(nmsg.Reply)
				applyMessageToNATS(out, rsp)
				out.Header.Set(rpc.HeaderUID, strconv.FormatInt(uid, 10))
				if clientInbox > 0 {
					out.Header.Set(rpc.HeaderClientInbox, strconv.Itoa(clientInbox))
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				return b.nc.PublishMsg(out)
			}
			return rpcruntime.ErrSessionResponderMissing
		},
		Push: b.Push,
	})
	delivery, done := b.newDelivery(route, session, msg)
	b.sendDelivery(context.Background(), delivery, done)
}

func (b *Bus) publishRequest(ctx context.Context, uid int64, clientInbox int, msg rpc.Message, reply string) error {
	if b.closing.Load() || b.closed.Load() {
		return ErrBusClosed
	}
	if msg.Name == "" {
		return rpc.ErrEmptyMessage
	}
	nmsg := natsgo.NewMsg(b.subject(msg.Name))
	applyMessageToNATS(nmsg, msg)
	nmsg.Header.Set(rpc.HeaderUID, strconv.FormatInt(uid, 10))
	if clientInbox > 0 {
		nmsg.Header.Set(rpc.HeaderClientInbox, strconv.Itoa(clientInbox))
	}
	nmsg.Reply = reply
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if err := b.nc.PublishMsg(nmsg); err != nil {
		return err
	}
	b.stats.RecordReqOut(msg.Name, len(msg.Payload))
	return nil
}

func (b *Bus) addSub(sub *natsgo.Subscription) {
	b.subsMu.Lock()
	b.subs = append(b.subs, sub)
	b.subsMu.Unlock()
}

func (b *Bus) drainSubscriptions(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	b.subsMu.Lock()
	subs := append([]*natsgo.Subscription(nil), b.subs...)
	b.subsMu.Unlock()
	var err error
	for _, sub := range subs {
		if sub == nil {
			continue
		}
		if subErr := sub.Drain(); subErr != nil && !errors.Is(subErr, natsgo.ErrConnectionClosed) {
			err = errors.Join(err, subErr)
		}
	}
	for {
		allClosed := true
		for _, sub := range subs {
			if sub != nil && sub.IsValid() {
				allClosed = false
				break
			}
		}
		if allClosed {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (b *Bus) closeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	timeout := b.cfg.Options.DrainTimeout
	if timeout <= 0 {
		timeout = natsgo.DefaultDrainTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func (b *Bus) newDelivery(route rpc.Route, session rpc.Session, msg rpc.Message) (*rpcruntime.Delivery, func()) {
	b.deliveryWG.Add(1)
	var once sync.Once
	done := func() {
		once.Do(func() {
			b.deliveryWG.Done()
		})
	}
	return rpcruntime.NewDelivery(route, session, msg, b.stats, done), done
}

func (b *Bus) sendDelivery(ctx context.Context, delivery *rpcruntime.Delivery, done func()) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			done()
			err = ErrBusClosed
		}
	}()
	select {
	case <-ctx.Done():
		done()
		return ctx.Err()
	case b.deliveries <- delivery:
		return nil
	}
}

func waitDeliveries(ctx context.Context, wg *sync.WaitGroup) error {
	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func waitNATSClosed(ctx context.Context, nc *natsgo.Conn) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for !nc.IsClosed() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	return nil
}

func (b *Bus) subject(name string) string {
	return b.cfg.SubjectPrefix + name
}

func (b *Bus) clientSubject(uid int64) string {
	return b.cfg.ClientSubjectPrefix + strconv.FormatInt(uid, 10)
}

func (b *Bus) streamSubject(name string) string {
	return b.cfg.StreamSubjectPrefix + name
}

func messageFromNATS(msg *natsgo.Msg, fallbackName string) rpc.Message {
	header := make(map[string]string, len(msg.Header))
	for key, values := range msg.Header {
		if len(values) > 0 {
			header[key] = values[0]
		}
	}
	name := header[rpc.HeaderResponse]
	if name == "" {
		name = fallbackName
	}
	return rpc.Message{
		Name:    name,
		Payload: append([]byte(nil), msg.Data...),
		Header:  header,
	}
}

func applyMessageToNATS(dst *natsgo.Msg, src rpc.Message) {
	dst.Data = append([]byte(nil), src.Payload...)
	if dst.Header == nil {
		dst.Header = natsgo.Header{}
	}
	for key, value := range src.Header {
		dst.Header.Set(key, value)
	}
	if src.Name != "" && dst.Header.Get(rpc.HeaderResponse) == "" {
		dst.Header.Set(rpc.HeaderResponse, src.Name)
	}
}

func parseHeaderInt64(header map[string]string, key string) int64 {
	if len(header) == 0 || header[key] == "" {
		return 0
	}
	value, err := strconv.ParseInt(header[key], 10, 64)
	if err != nil {
		return 0
	}
	return value
}

// String 返回 NATS bus 连接的简短诊断描述。
func (b *Bus) String() string {
	if b == nil || b.nc == nil {
		return "nats rpc bus <nil>"
	}
	return fmt.Sprintf("nats rpc bus %s", b.nc.ConnectedAddr())
}
