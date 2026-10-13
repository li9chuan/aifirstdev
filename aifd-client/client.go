package client

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	observe "github.com/li9chuan/aifirstdev/aifd-observe/contracts"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	"google.golang.org/protobuf/proto"
)

const defaultTimeout = 15 * time.Second

var (
	errorType = reflect.TypeOf((*error)(nil)).Elem()
	protoType = reflect.TypeOf((*proto.Message)(nil)).Elem()
)

// ErrAwaitTimeout 表示客户端 await 等待回包超时。
var ErrAwaitTimeout = errors.New("aifd client await timeout")

// ErrClosed 表示使用了已关闭的客户端连接。
var ErrClosed = errors.New("aifd client closed")

// ErrNotStarted 表示 await 调用发生在客户端读取和超时循环启动前。
var ErrNotStarted = errors.New("aifd client not started")

// Transport 是客户端侧连接抽象，Wails、robot 或其他客户端只需适配这一层。
type Transport interface {
	Send(context.Context, rpc.Message) error
	Recv(context.Context) (rpc.Message, error)
	Close(context.Context) error
}

// Options 配置客户端侧 protobuf 调用门面。
type Options struct {
	AwaitTimeout time.Duration
	Observe      observe.Sink
}

// StatsSnapshot 是 Client 运行期计数的无锁快照。
type StatsSnapshot struct {
	Send    int64
	Receive int64
	Error   int64
}

// Client 封装客户端侧 protobuf Call/On、await inbox、回包匹配和 push 路由。
type Client struct {
	transport Transport
	router    *messageRouter
	callbacks *callbackCache
	observe   observe.Sink
	ctx       context.Context
	cancel    context.CancelFunc

	clientInboxSeq atomic.Uint64
	sendCount      atomic.Int64
	receiveCount   atomic.Int64
	errorCount     atomic.Int64
	closed         atomic.Bool
	started        atomic.Bool
	startOnce      sync.Once
	closeOnce      sync.Once
	terminalOnce   sync.Once
	lifecycleMu    sync.Mutex
	terminalErrMu  sync.RWMutex
	terminalErr    error
	done           chan struct{}
	wg             sync.WaitGroup
	heartbeatMu    sync.Mutex
	heartbeat      *heartbeatRun
}

// New 创建客户端侧 protobuf 调用门面。
func New(transport Transport, opts Options) (*Client, error) {
	if transport == nil {
		return nil, fmt.Errorf("aifd client transport is nil")
	}
	if opts.AwaitTimeout <= 0 {
		opts.AwaitTimeout = defaultTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		transport: transport,
		router:    newMessageRouter(),
		callbacks: newCallbackCache(opts.AwaitTimeout),
		observe:   opts.Observe,
		ctx:       ctx,
		cancel:    cancel,
		done:      make(chan struct{}),
	}, nil
}

// Call 发送 protobuf 请求；topic 从请求 protobuf 名称推导。
// 不传 callback 时只发送，传 func(*Rsp, error) 时异步等待回包。
func (c *Client) Call(req proto.Message, args ...any) error {
	return c.callAs("", req, args...)
}

// CallAs sends a protobuf request using an explicit route topic.
// It is intended for business protocols whose route name differs from the request message name.
func (c *Client) CallAs(topic string, req proto.Message, args ...any) error {
	if topic == "" {
		return fmt.Errorf("aifd client call topic is empty")
	}
	return c.callAs(topic, req, args...)
}

func (c *Client) callAs(topic string, req proto.Message, args ...any) error {
	if len(args) > 1 {
		return fmt.Errorf("aifd client call accepts at most one callback")
	}
	var handler func(proto.Message, error)
	var rspType reflect.Type
	if len(args) == 1 {
		var err error
		handler, rspType, err = buildCallHandler(args[0])
		if err != nil {
			return err
		}
	}
	msg, err := rpc.NewMessagePB(topic, req)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return c.sendMessage(msg)
	}
	if err := c.checkAwaitReady(); err != nil {
		return err
	}
	return c.requestMessage(msg, func(response rpc.Message, err error) error {
		if err != nil {
			handler(nil, err)
			return nil
		}
		if err := rpc.StatusFromMessage(response); err != nil {
			handler(nil, err)
			return err
		}
		rsp := reflect.New(rspType.Elem()).Interface().(proto.Message)
		if err := response.DecodePB(rsp); err != nil {
			handler(nil, err)
			return err
		}
		handler(rsp, nil)
		return nil
	})
}

// On 注册无 client inbox 的服务端 protobuf push handler；topic 从 handler 入参 protobuf 名称推导。
func (c *Client) On(handler any) error {
	topic, routeHandler, err := buildPushHandler(handler)
	if err != nil {
		return err
	}
	c.router.Register(topic, routeHandler)
	return nil
}

// OnAs registers a typed push handler using an explicit route topic.
func (c *Client) OnAs(topic string, handler any) error {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return fmt.Errorf("aifd client push topic is empty")
	}
	_, routeHandler, err := buildPushHandler(handler)
	if err != nil {
		return err
	}
	c.router.Register(topic, routeHandler)
	return nil
}

// Request sends a protobuf request using its message name and blocks for a matched reply.
func (c *Client) Request(ctx context.Context, req proto.Message, rsp proto.Message) error {
	return c.requestAs(ctx, "", req, rsp)
}

// RequestAs sends a protobuf request using an explicit route topic and blocks for a matched reply.
func (c *Client) RequestAs(ctx context.Context, topic string, req proto.Message, rsp proto.Message) error {
	if strings.TrimSpace(topic) == "" {
		return fmt.Errorf("aifd client request topic is empty")
	}
	return c.requestAs(ctx, topic, req, rsp)
}

func (c *Client) requestAs(ctx context.Context, topic string, req proto.Message, rsp proto.Message) error {
	if req == nil || rsp == nil {
		return fmt.Errorf("aifd client request and response protobuf are required")
	}
	if err := c.checkAwaitReady(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	msg, err := rpc.NewMessagePB(topic, req)
	if err != nil {
		return err
	}
	inbox := c.nextClientInbox()
	if msg.Header == nil {
		msg.Header = make(map[string]string, 1)
	}
	msg.Header[rpc.HeaderClientInbox] = inbox
	done := make(chan error, 1)
	handler := func(response rpc.Message, err error) error {
		if err == nil {
			err = rpc.StatusFromMessage(response)
		}
		if err == nil {
			err = response.DecodePB(rsp)
		}
		done <- err
		return err
	}
	if err := c.registerAwait(inbox, handler); err != nil {
		return err
	}
	if err := c.sendMessage(msg); err != nil {
		if callback, ok := c.callbacks.Pop(inbox); ok {
			c.fireCallbacks([]callbackHandler{callback}, rpc.Message{}, err)
		}
		return err
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		c.callbacks.Pop(inbox)
		return ctx.Err()
	case <-c.done:
		select {
		case err := <-done:
			return err
		default:
			return c.Err()
		}
	}
}

// Start 启动客户端读取和 await 超时清理循环；重复调用只启动一次。
func (c *Client) Start() error {
	if c.closed.Load() {
		return ErrClosed
	}
	c.startOnce.Do(func() {
		c.wg.Add(2)
		go c.readLoop()
		go c.timeoutLoop()
		c.started.Store(true)
	})
	return nil
}

// Close 停止读取循环、唤醒 pending await，并关闭底层 transport。
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.StopHeartbeat()
		c.signalTermination(ErrClosed)
		err = c.transport.Close(context.Background())
		c.wg.Wait()
	})
	return err
}

// Done closes when the transport terminates or Close is called.
func (c *Client) Done() <-chan struct{} {
	if c == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return c.done
}

// Err returns the terminal transport error. Intentional Close returns ErrClosed.
func (c *Client) Err() error {
	if c == nil {
		return ErrClosed
	}
	c.terminalErrMu.RLock()
	defer c.terminalErrMu.RUnlock()
	return c.terminalErr
}

// Stats 返回客户端 send、receive 和 error 计数快照。
func (c *Client) Stats() StatsSnapshot {
	return StatsSnapshot{
		Send:    c.sendCount.Load(),
		Receive: c.receiveCount.Load(),
		Error:   c.errorCount.Load(),
	}
}

func (c *Client) sendMessage(msg rpc.Message) error {
	if c.closed.Load() {
		return ErrClosed
	}
	if err := c.transport.Send(c.ctx, msg.Clone()); err != nil {
		c.addError(1)
		return err
	}
	c.sendCount.Add(1)
	c.writeObserve("client_send", 1, map[string]string{"message": msg.Name})
	return nil
}

func (c *Client) requestMessage(msg rpc.Message, handler callbackHandler) error {
	if err := c.checkAwaitReady(); err != nil {
		return err
	}

	inbox := c.nextClientInbox()
	request := msg.Clone()
	if request.Header == nil {
		request.Header = make(map[string]string, 1)
	}
	request.Header[rpc.HeaderClientInbox] = inbox

	if err := c.registerAwait(inbox, handler); err != nil {
		return err
	}
	if err := c.sendMessage(request); err != nil {
		if callback, ok := c.callbacks.Pop(inbox); ok {
			c.fireCallbacks([]callbackHandler{callback}, rpc.Message{}, err)
		}
		return err
	}
	return nil
}

func (c *Client) registerAwait(inbox string, handler callbackHandler) error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if err := c.checkAwaitReady(); err != nil {
		return err
	}
	c.callbacks.Add(inbox, handler)
	return nil
}

func (c *Client) readLoop() {
	defer c.wg.Done()
	for {
		if c.closed.Load() {
			return
		}
		msg, err := c.transport.Recv(c.ctx)
		if err != nil {
			if !c.closed.Load() && !errors.Is(err, context.Canceled) {
				c.addError(1)
				c.signalTermination(err)
				_ = c.transport.Close(context.Background())
			}
			return
		}
		c.receiveCount.Add(1)
		if inbox := clientInbox(msg); inbox != "" {
			if handler, ok := c.callbacks.Pop(inbox); ok {
				if err := handler(msg, nil); err != nil {
					c.addError(1)
				}
			} else {
				c.recordStaleReply(msg, inbox)
			}
			continue
		}
		if _, err := c.router.Route(c.ctx, msg); err != nil {
			c.addError(1)
		}
		c.writeObserve("client_receive", 1, map[string]string{"message": msg.Name})
	}
}

func (c *Client) signalTermination(err error) {
	if err == nil {
		err = ErrClosed
	}
	c.terminalOnce.Do(func() {
		c.lifecycleMu.Lock()
		c.closed.Store(true)
		c.terminalErrMu.Lock()
		c.terminalErr = err
		c.terminalErrMu.Unlock()
		c.cancel()
		callbacks := c.callbacks.Clear()
		close(c.done)
		c.lifecycleMu.Unlock()
		c.fireCallbacks(callbacks, rpc.Message{}, err)
	})
}

func (c *Client) timeoutLoop() {
	defer c.wg.Done()
	for {
		next, ok := c.callbacks.NextExpire()
		if !ok {
			select {
			case <-c.ctx.Done():
				return
			case <-c.callbacks.Notify():
				continue
			}
		}
		wait := time.Until(next)
		if wait <= 0 {
			c.fireCallbacks(c.callbacks.PopExpired(time.Now()), rpc.Message{}, ErrAwaitTimeout)
			continue
		}
		timer := time.NewTimer(wait)
		select {
		case <-c.ctx.Done():
			stopTimer(timer)
			return
		case <-c.callbacks.Notify():
			stopTimer(timer)
		case <-timer.C:
			c.fireCallbacks(c.callbacks.PopExpired(time.Now()), rpc.Message{}, ErrAwaitTimeout)
		}
	}
}

func buildCallHandler(cb any) (func(proto.Message, error), reflect.Type, error) {
	if cb == nil {
		return nil, nil, fmt.Errorf("aifd client call callback is nil")
	}
	value := reflect.ValueOf(cb)
	fnType := value.Type()
	if fnType.Kind() != reflect.Func {
		return nil, nil, fmt.Errorf("aifd client call callback must be func(*Rsp, error), got %s", fnType.Kind())
	}
	if value.IsNil() {
		return nil, nil, fmt.Errorf("aifd client call callback is nil")
	}
	if fnType.NumIn() != 2 || fnType.NumOut() != 0 {
		return nil, nil, fmt.Errorf("aifd client call callback must be func(*Rsp, error)")
	}
	rspType := fnType.In(0)
	if rspType.Kind() != reflect.Ptr || !rspType.Implements(protoType) {
		return nil, nil, fmt.Errorf("aifd client call callback response must be protobuf pointer")
	}
	if fnType.In(1) != errorType {
		return nil, nil, fmt.Errorf("aifd client call callback second argument must be error")
	}
	handler := func(rsp proto.Message, err error) {
		args := []reflect.Value{reflect.Zero(rspType), reflect.Zero(fnType.In(1))}
		if rsp != nil {
			args[0] = reflect.ValueOf(rsp)
		}
		if err != nil {
			args[1] = reflect.ValueOf(err)
		}
		value.Call(args)
	}
	return handler, rspType, nil
}

func buildPushHandler(handler any) (string, pushHandler, error) {
	if handler == nil {
		return "", nil, fmt.Errorf("aifd client on handler is nil")
	}
	value := reflect.ValueOf(handler)
	fnType := value.Type()
	if fnType.Kind() != reflect.Func {
		return "", nil, fmt.Errorf("aifd client on handler must be func(*Msg), got %s", fnType.Kind())
	}
	if value.IsNil() {
		return "", nil, fmt.Errorf("aifd client on handler is nil")
	}
	if fnType.NumIn() != 1 || fnType.NumOut() != 0 {
		return "", nil, fmt.Errorf("aifd client on handler must be func(*Msg)")
	}
	msgType := fnType.In(0)
	if msgType.Kind() != reflect.Ptr || !msgType.Implements(protoType) {
		return "", nil, fmt.Errorf("aifd client on handler message must be protobuf pointer")
	}
	msgProto := reflect.New(msgType.Elem()).Interface().(proto.Message)
	topic := string(msgProto.ProtoReflect().Descriptor().Name())
	return topic, func(_ context.Context, msg rpc.Message) error {
		push := reflect.New(msgType.Elem()).Interface().(proto.Message)
		if err := msg.DecodePB(push); err != nil {
			return err
		}
		value.Call([]reflect.Value{reflect.ValueOf(push)})
		return nil
	}, nil
}

func clientInbox(msg rpc.Message) string {
	if len(msg.Header) > 0 {
		return msg.Header[rpc.HeaderClientInbox]
	}
	return ""
}

func (c *Client) checkReady() error {
	select {
	case <-c.ctx.Done():
		return ErrClosed
	default:
		return nil
	}
}

func (c *Client) checkAwaitReady() error {
	if err := c.checkReady(); err != nil {
		return err
	}
	if !c.started.Load() {
		return ErrNotStarted
	}
	return nil
}

func (c *Client) recordStaleReply(msg rpc.Message, inbox string) {
	c.addError(1)
	c.writeObserve("client_stale_reply", 1, map[string]string{
		"message": msg.Name,
		"inbox":   inbox,
	})
}

func (c *Client) addError(delta int64) {
	c.errorCount.Add(delta)
}

func (c *Client) nextClientInbox() string {
	return strconv.FormatUint(c.clientInboxSeq.Add(1), 10)
}

func (c *Client) fireCallbacks(callbacks []callbackHandler, msg rpc.Message, err error) {
	for _, callback := range callbacks {
		if callbackErr := callback(msg, err); callbackErr != nil {
			c.addError(1)
		}
	}
}

func (c *Client) writeObserve(name string, value float64, dimensions map[string]string) {
	if c.observe == nil {
		return
	}
	_ = c.observe.Write(c.ctx, []observe.Event{observe.Metric(name, value, dimensions)})
}

func stopTimer(timer *time.Timer) {
	if timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}
