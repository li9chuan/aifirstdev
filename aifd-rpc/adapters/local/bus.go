package local

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	rpcruntime "github.com/li9chuan/aifirstdev/aifd-rpc/runtime"
	"google.golang.org/protobuf/proto"
)

// ErrBusClosed 表示使用了已关闭的 local bus。
var ErrBusClosed = errors.New("local rpc bus closed")

// Config 控制 local bus 缓冲、超时和统计标签。
type Config struct {
	ServiceName    string
	Buffer         int
	DefaultTimeout time.Duration
	StatsWindow    time.Duration
}

// Bus 是进程内 RPC bus；不使用网络 transport，但保持与远程 adapter 相同的公开行为。
type Bus struct {
	router     *rpcruntime.Router
	deliveries chan rpc.Delivery
	callbacks  *rpcruntime.CallbackCache
	stats      *rpcruntime.StatsCollector
	closed     atomic.Bool
	seq        atomic.Uint64
	runner     atomic.Bool
	deliveryWG sync.WaitGroup

	mu     sync.Mutex
	pushes []PushedMessage
}

// NewBus 创建带 local router、callback cache 和 stats 的进程内 bus。
func NewBus(cfg Config) *Bus {
	if cfg.Buffer <= 0 {
		cfg.Buffer = 1024
	}
	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = 15 * time.Second
	}
	return &Bus{
		router:     rpcruntime.NewRouter(),
		deliveries: make(chan rpc.Delivery, cfg.Buffer),
		callbacks:  rpcruntime.NewCallbackCache(cfg.DefaultTimeout),
		stats:      rpcruntime.NewStatsCollector(cfg.ServiceName, cfg.StatsWindow),
	}
}

// Register 添加直接 local route。
func (b *Bus) Register(route rpc.Route) error {
	return b.router.Register(route)
}

// RegisterQueue 添加 local queue route，并保留 queue 元数据。
func (b *Bus) RegisterQueue(route rpc.Route, queue string) error {
	return b.router.RegisterQueue(route, queue)
}

// RegisterStream 向进程内 router 添加 local stream route。
func (b *Bus) RegisterStream(_ context.Context, route rpc.Route) error {
	return b.router.RegisterStream(route)
}

// Tell 为目标 uid 投递单向消息。
func (b *Bus) Tell(ctx context.Context, uid int64, msg rpc.Message) error {
	return b.enqueue(ctx, uid, "", 0, msg)
}

// TellPB marshal protobuf 请求，并通过 Tell 发送。
func (b *Bus) TellPB(ctx context.Context, uid int64, name string, pb proto.Message) error {
	msg, err := rpc.NewMessagePB(name, pb)
	if err != nil {
		return err
	}
	return b.Tell(ctx, uid, msg)
}

// Call 发送消息，并在提供 cb 时存储 callback。
func (b *Bus) Call(ctx context.Context, uid int64, msg rpc.Message, cb rpc.Handler, opts rpc.CallOptions) error {
	if cb == nil {
		return b.Tell(ctx, uid, msg)
	}
	inbox := b.nextInbox()
	if err := b.callbacks.Add(rpcruntime.CallbackEntry{
		Inbox:     inbox,
		RouteName: msg.Name,
		UID:       uid,
		Handler:   cb,
		OnTimeout: opts.OnTimeout,
		Timeout:   opts.Timeout,
	}); err != nil {
		return err
	}
	if err := b.enqueue(ctx, uid, inbox, 0, msg); err != nil {
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

// Request 发送消息，并等待一个响应或超时。
func (b *Bus) Request(ctx context.Context, uid int64, msg rpc.Message, opts rpc.RequestOptions) (rpc.Message, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
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

// Push 为目标 uid 记录克隆后的下行消息。
func (b *Bus) Push(_ context.Context, uid int64, msg rpc.Message) error {
	if b.closed.Load() {
		return ErrBusClosed
	}
	out := msg.Clone()
	if out.Header == nil {
		out.Header = map[string]string{}
	}
	if out.Header[rpc.HeaderResponse] == "" {
		out.Header[rpc.HeaderResponse] = out.Name
	}
	b.mu.Lock()
	b.pushes = append(b.pushes, PushedMessage{UID: uid, Message: out})
	b.mu.Unlock()
	return nil
}

// PushPB marshal protobuf push 消息，并通过 Push 记录。
func (b *Bus) PushPB(ctx context.Context, uid int64, pb proto.Message) error {
	msg, err := rpc.NewMessagePB("", pb)
	if err != nil {
		return err
	}
	return b.Push(ctx, uid, msg)
}

// SaveToStream 将 stream 风格消息投递到 local delivery channel。
func (b *Bus) SaveToStream(ctx context.Context, uid int64, msg rpc.Message) error {
	return b.enqueue(ctx, uid, "", 0, msg)
}

// SavePBToStream marshal protobuf stream 事件，并通过 SaveToStream 发送。
func (b *Bus) SavePBToStream(ctx context.Context, uid int64, name string, pb proto.Message) error {
	msg, err := rpc.NewMessagePB(name, pb)
	if err != nil {
		return err
	}
	return b.SaveToStream(ctx, uid, msg)
}

// Deliveries 向 runner 暴露 local handler delivery channel。
func (b *Bus) Deliveries() <-chan rpc.Delivery {
	b.runner.Store(true)
	return b.deliveries
}

// Stats 返回 local bus stats collector。
func (b *Bus) Stats() rpc.Stats {
	return b.stats
}

// Close 关闭 callback 并关闭 delivery channel。
func (b *Bus) Close(ctx context.Context) error {
	if b.closed.Swap(true) {
		return nil
	}
	b.callbacks.Close()
	close(b.deliveries)
	if !b.runner.Load() {
		return nil
	}
	return waitDeliveries(ctx, &b.deliveryWG)
}

// Pushes 返回 Push 记录的下行消息克隆。
func (b *Bus) Pushes() []PushedMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]PushedMessage, len(b.pushes))
	for i := range b.pushes {
		out[i] = PushedMessage{UID: b.pushes[i].UID, Message: b.pushes[i].Message.Clone()}
	}
	return out
}

func (b *Bus) enqueue(ctx context.Context, uid int64, inbox string, clientInbox int, msg rpc.Message) error {
	if b.closed.Load() {
		return ErrBusClosed
	}
	if msg.Name == "" {
		return rpc.ErrEmptyMessage
	}
	route, ok := b.router.Get(msg.Name)
	if !ok {
		return fmt.Errorf("rpc route %s not found", msg.Name)
	}
	out := msg.Clone()
	if out.Header == nil {
		out.Header = map[string]string{}
	}
	out.Header[rpc.HeaderUID] = strconv.FormatInt(uid, 10)
	if clientInbox > 0 {
		out.Header[rpc.HeaderClientInbox] = strconv.Itoa(clientInbox)
	}
	effectiveClientInbox := clientInbox
	if effectiveClientInbox == 0 {
		effectiveClientInbox = parseClientInbox(out.Header)
	}
	b.stats.RecordReqOut(route.Name, len(out.Payload))
	b.stats.RecordReqIn(route.Name, len(out.Payload))

	session := rpcruntime.NewSession(rpcruntime.SessionOptions{
		UID:         uid,
		Topic:       msg.Name,
		Inbox:       inbox,
		ClientInbox: effectiveClientInbox,
		Respond: func(ctx context.Context, rsp rpc.Message) error {
			b.stats.RecordRspOut(route.Name, len(rsp.Payload))
			if inbox != "" {
				rspSession := rpcruntime.NewSession(rpcruntime.SessionOptions{UID: uid, Topic: rsp.Name, Inbox: inbox})
				ok, err := b.callbacks.Resolve(ctx, inbox, rspSession, rsp)
				if ok {
					b.stats.RecordRspIn(route.Name, len(rsp.Payload))
				}
				return err
			}
			if effectiveClientInbox > 0 {
				if rsp.Header == nil {
					rsp.Header = map[string]string{}
				}
				rsp.Header[rpc.HeaderClientInbox] = strconv.Itoa(effectiveClientInbox)
				return b.Push(ctx, uid, rsp)
			}
			return nil
		},
		Push: b.Push,
	})
	delivery, done := b.newDelivery(route, session, out)
	return b.sendDelivery(ctx, delivery, done)
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

func (b *Bus) nextInbox() string {
	return "_LOCAL_INBOX." + strconv.FormatUint(b.seq.Add(1), 10)
}

func parseClientInbox(header map[string]string) int {
	if len(header) == 0 || header[rpc.HeaderClientInbox] == "" {
		return 0
	}
	value, err := strconv.Atoi(header[rpc.HeaderClientInbox])
	if err != nil {
		return 0
	}
	return value
}
