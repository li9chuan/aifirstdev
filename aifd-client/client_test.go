package client

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-observe/adapters/memory"
	observe "github.com/li9chuan/aifirstdev/aifd-observe/contracts"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestOnRoutesTypedPush(t *testing.T) {
	transport := newTestTransport()
	client := newTestClient(t, transport, Options{})
	handled := make(chan string, 1)
	if err := client.On(func(msg *wrapperspb.StringValue) {
		handled <- msg.GetValue()
	}); err != nil {
		t.Fatalf("On error = %v", err)
	}
	if err := client.Start(); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	transport.Inject(newStringMessage(t, "StringValue", "push"))
	select {
	case value := <-handled:
		if value != "push" {
			t.Fatalf("push value = %q, want push", value)
		}
	case <-time.After(time.Second):
		t.Fatal("typed push handler was not called")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
}

func TestCallMatchesClientInbox(t *testing.T) {
	transport := newTestTransport()
	client := newTestClient(t, transport, Options{})
	if err := client.Start(); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer func() { _ = client.Close() }()

	done := make(chan requestResult, 1)
	if err := client.Call(wrapperspb.String("req"), func(rsp *wrapperspb.StringValue, err error) {
		if err != nil {
			done <- requestResult{err: err}
			return
		}
		done <- requestResult{value: rsp.GetValue()}
	}); err != nil {
		t.Fatalf("Call error = %v", err)
	}

	sent := waitForSent(t, transport, 1)
	if sent[0].Name != "StringValue" {
		t.Fatalf("message name = %q, want protobuf type name", sent[0].Name)
	}
	inbox := sent[0].Header[rpc.HeaderClientInbox]
	if value, err := strconv.Atoi(inbox); err != nil || value <= 0 {
		t.Fatalf("client inbox = %q, want positive integer", inbox)
	}
	rsp := newStringMessage(t, "MseEcho", "rsp")
	rsp.Header[rpc.HeaderClientInbox] = inbox
	transport.Inject(rsp)

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("Call callback error = %v", result.err)
		}
		if result.value != "rsp" {
			t.Fatalf("response = %q, want rsp", result.value)
		}
	case <-time.After(time.Second):
		t.Fatal("Call callback did not complete")
	}
}

func TestRequestAsSeparatesReplyFromPushAndSignalsDisconnect(t *testing.T) {
	transport := newTestTransport()
	client := newTestClient(t, transport, Options{})
	push := make(chan string, 1)
	if err := client.OnAs("MsePush", func(msg *wrapperspb.StringValue) {
		push <- msg.GetValue()
	}); err != nil {
		t.Fatalf("OnAs error = %v", err)
	}
	if err := client.Start(); err != nil {
		t.Fatalf("Start error = %v", err)
	}

	rsp := &wrapperspb.StringValue{}
	requestDone := make(chan error, 1)
	go func() {
		requestDone <- client.RequestAs(context.Background(), "MceRequest", wrapperspb.String("req"), rsp)
	}()
	sent := waitForSent(t, transport, 1)
	inbox := sent[0].Header[rpc.HeaderClientInbox]
	transport.Inject(newStringMessage(t, "MsePush", "push"))
	reply := newStringMessage(t, "MseResponse", "reply")
	reply.Header[rpc.HeaderClientInbox] = inbox
	transport.Inject(reply)

	select {
	case value := <-push:
		if value != "push" {
			t.Fatalf("push = %q, want push", value)
		}
	case <-time.After(time.Second):
		t.Fatal("push was not routed")
	}
	select {
	case err := <-requestDone:
		if err != nil || rsp.GetValue() != "reply" {
			t.Fatalf("RequestAs err=%v response=%q", err, rsp.GetValue())
		}
	case <-time.After(time.Second):
		t.Fatal("RequestAs did not complete")
	}

	if err := transport.Close(context.Background()); err != nil {
		t.Fatalf("transport Close error = %v", err)
	}
	select {
	case <-client.Done():
		if !errors.Is(client.Err(), ErrClosed) {
			t.Fatalf("client Err = %v, want ErrClosed", client.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("client did not signal disconnect")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("client Close error = %v", err)
	}
}

func TestCallStatusResponseReturnsCallbackError(t *testing.T) {
	transport := newTestTransport()
	client := newTestClient(t, transport, Options{})
	if err := client.Start(); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer func() { _ = client.Close() }()

	done := make(chan requestResult, 1)
	if err := client.Call(wrapperspb.String("req"), func(rsp *wrapperspb.StringValue, err error) {
		result := requestResult{err: err}
		if rsp != nil {
			result.value = rsp.GetValue()
		}
		done <- result
	}); err != nil {
		t.Fatalf("Call error = %v", err)
	}

	sent := waitForSent(t, transport, 1)
	rsp := rpc.Message{
		Name:    "RPCError",
		Payload: []byte("not protobuf"),
		Header: map[string]string{
			rpc.HeaderClientInbox:  sent[0].Header[rpc.HeaderClientInbox],
			rpc.HeaderStatus:       "INVALID",
			rpc.HeaderErrorMessage: "bad request",
		},
	}
	transport.Inject(rsp)

	select {
	case result := <-done:
		var statusErr *rpc.StatusError
		if !errors.As(result.err, &statusErr) {
			t.Fatalf("Call callback error = %v, want StatusError", result.err)
		}
		if statusErr.Code != "INVALID" || statusErr.Message != "bad request" {
			t.Fatalf("status error = %+v, want INVALID bad request", statusErr)
		}
		if result.value != "" {
			t.Fatalf("callback response value = %q, want empty", result.value)
		}
	case <-time.After(time.Second):
		t.Fatal("Call callback did not complete")
	}
}

func TestCallWithoutCallbackDoesNotAllocateClientInbox(t *testing.T) {
	transport := newTestTransport()
	client := newTestClient(t, transport, Options{})
	if err := client.Call(wrapperspb.String("req")); err != nil {
		t.Fatalf("Call error = %v", err)
	}
	sent := waitForSent(t, transport, 1)
	if sent[0].Name != "StringValue" {
		t.Fatalf("message name = %q, want protobuf type name", sent[0].Name)
	}
	if got := sent[0].Header[rpc.HeaderClientInbox]; got != "" {
		t.Fatalf("client inbox = %q, want empty", got)
	}
}

func TestCallInvalidHandlerDoesNotSend(t *testing.T) {
	transport := newTestTransport()
	client := newTestClient(t, transport, Options{})
	err := client.Call(wrapperspb.String("req"), func(*wrapperspb.StringValue) {})
	if err == nil {
		t.Fatal("Call error = nil, want invalid handler error")
	}
	if sent := transport.Sent(); len(sent) != 0 {
		t.Fatalf("sent count = %d, want 0", len(sent))
	}
}

func TestCallNilRequestDoesNotSend(t *testing.T) {
	transport := newTestTransport()
	client := newTestClient(t, transport, Options{})
	if err := client.Call(nil); err == nil {
		t.Fatal("Call nil request error = nil")
	}
	if sent := transport.Sent(); len(sent) != 0 {
		t.Fatalf("sent count = %d, want 0", len(sent))
	}
}

func TestCallWithCallbackBeforeStartDoesNotSend(t *testing.T) {
	transport := newTestTransport()
	client := newTestClient(t, transport, Options{})

	err := client.Call(wrapperspb.String("req"), func(*wrapperspb.StringValue, error) {})
	if !errors.Is(err, ErrNotStarted) {
		t.Fatalf("Call error = %v, want ErrNotStarted", err)
	}
	if sent := transport.Sent(); len(sent) != 0 {
		t.Fatalf("sent count = %d, want 0", len(sent))
	}
	if got := client.callbacks.Len(); got != 0 {
		t.Fatalf("callback cache len = %d, want 0", got)
	}
}

func TestCallWithCallbackAfterCloseDoesNotRegister(t *testing.T) {
	transport := newTestTransport()
	client := newTestClient(t, transport, Options{})
	if err := client.Start(); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	callback := make(chan error, 1)
	err := client.Call(wrapperspb.String("req"), func(_ *wrapperspb.StringValue, err error) {
		callback <- err
	})
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("Call error = %v, want ErrClosed", err)
	}
	if sent := transport.Sent(); len(sent) != 0 {
		t.Fatalf("sent count = %d, want 0", len(sent))
	}
	if got := client.callbacks.Len(); got != 0 {
		t.Fatalf("callback cache len = %d, want 0", got)
	}
	select {
	case err := <-callback:
		t.Fatalf("callback fired after closed Call: %v", err)
	default:
	}
}

func TestCallMatchesConcurrentRepliesByInbox(t *testing.T) {
	transport := newTestTransport()
	client := newTestClient(t, transport, Options{})
	if err := client.Start(); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer func() { _ = client.Close() }()

	results := make(chan requestResult, 2)
	for _, value := range []string{"one", "two"} {
		value := value
		if err := client.Call(wrapperspb.String(value), func(rsp *wrapperspb.StringValue, err error) {
			result := requestResult{name: value, err: err}
			if rsp != nil {
				result.value = rsp.GetValue()
			}
			results <- result
		}); err != nil {
			t.Fatalf("Call %s error = %v", value, err)
		}
	}

	sent := waitForSent(t, transport, 2)
	inboxes := map[string]string{}
	for _, msg := range sent {
		var req wrapperspb.StringValue
		if err := msg.DecodePB(&req); err != nil {
			t.Fatalf("request decode error = %v", err)
		}
		inboxes[req.GetValue()] = msg.Header[rpc.HeaderClientInbox]
	}
	if inboxes["one"] == "" || inboxes["two"] == "" {
		t.Fatalf("missing inboxes: %+v", inboxes)
	}
	if inboxes["one"] == inboxes["two"] {
		t.Fatalf("duplicate inbox %q", inboxes["one"])
	}

	for _, value := range []string{"two", "one"} {
		rsp := newStringMessage(t, "MseEcho", "rsp-"+value)
		rsp.Header[rpc.HeaderClientInbox] = inboxes[value]
		transport.Inject(rsp)
	}

	got := map[string]string{}
	for i := 0; i < 2; i++ {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatalf("Call %s error = %v", result.name, result.err)
			}
			got[result.name] = result.value
		case <-time.After(time.Second):
			t.Fatal("concurrent Call callbacks did not complete")
		}
	}
	if got["one"] != "rsp-one" || got["two"] != "rsp-two" {
		t.Fatalf("responses = %+v, want per-inbox replies", got)
	}
}

func TestCallTimeoutRemovesCallback(t *testing.T) {
	transport := newTestTransport()
	recorder := memory.NewRecorder()
	client := newTestClient(t, transport, Options{AwaitTimeout: 10 * time.Millisecond, Observe: recorder})
	routeHit := make(chan struct{}, 1)
	if err := client.On(func(*wrapperspb.StringValue) {
		routeHit <- struct{}{}
	}); err != nil {
		t.Fatalf("On error = %v", err)
	}
	if err := client.Start(); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer func() { _ = client.Close() }()

	done := make(chan error, 1)
	if err := client.Call(wrapperspb.String("req"), func(_ *wrapperspb.StringValue, err error) {
		done <- err
	}); err != nil {
		t.Fatalf("Call error = %v", err)
	}

	sent := waitForSent(t, transport, 1)
	inbox := sent[0].Header[rpc.HeaderClientInbox]
	select {
	case err := <-done:
		if !errors.Is(err, ErrAwaitTimeout) {
			t.Fatalf("Call callback error = %v, want ErrAwaitTimeout", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Call timeout did not complete")
	}

	rsp := newStringMessage(t, "MseTimeout", "late")
	rsp.Header[rpc.HeaderClientInbox] = inbox
	transport.Inject(rsp)
	select {
	case <-routeHit:
		t.Fatal("late response was routed after request timeout")
	case <-time.After(20 * time.Millisecond):
	}
	if got := client.Stats().Error; got != 1 {
		t.Fatalf("client error = %d, want 1 stale reply", got)
	}
	if !hasMetric(recorder.Events(), "client_stale_reply", inbox) {
		t.Fatalf("observe events = %+v, want client_stale_reply for inbox %s", recorder.Events(), inbox)
	}
}

func TestCallReplyRemovesCallbackBeforeTimeout(t *testing.T) {
	transport := newTestTransport()
	client := newTestClient(t, transport, Options{AwaitTimeout: 20 * time.Millisecond})
	if err := client.Start(); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer func() { _ = client.Close() }()

	done := make(chan error, 2)
	if err := client.Call(wrapperspb.String("req"), func(_ *wrapperspb.StringValue, err error) {
		done <- err
	}); err != nil {
		t.Fatalf("Call error = %v", err)
	}
	sent := waitForSent(t, transport, 1)
	rsp := newStringMessage(t, "MseEcho", "rsp")
	rsp.Header[rpc.HeaderClientInbox] = sent[0].Header[rpc.HeaderClientInbox]
	transport.Inject(rsp)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Call callback error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Call callback did not complete")
	}
	select {
	case err := <-done:
		t.Fatalf("Call callback fired twice, second error = %v", err)
	case <-time.After(40 * time.Millisecond):
	}
}

func TestCallReturnsWhenClientCloses(t *testing.T) {
	transport := newTestTransport()
	client := newTestClient(t, transport, Options{AwaitTimeout: time.Hour})
	if err := client.Start(); err != nil {
		t.Fatalf("Start error = %v", err)
	}

	done := make(chan error, 1)
	if err := client.Call(wrapperspb.String("req"), func(_ *wrapperspb.StringValue, err error) {
		done <- err
	}); err != nil {
		t.Fatalf("Call error = %v", err)
	}
	waitForSent(t, transport, 1)
	if err := client.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("Call callback error = %v, want ErrClosed", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Call did not return after client close")
	}
}

func TestCallCloseDuringSendReturnsClosedCallback(t *testing.T) {
	transport := newBlockingSendTransport()
	client := newTestClient(t, transport, Options{AwaitTimeout: time.Hour})
	if err := client.Start(); err != nil {
		t.Fatalf("Start error = %v", err)
	}

	callDone := make(chan error, 1)
	callback := make(chan error, 1)
	go func() {
		callDone <- client.Call(wrapperspb.String("req"), func(_ *wrapperspb.StringValue, err error) {
			callback <- err
		})
	}()
	select {
	case <-transport.entered:
	case <-time.After(time.Second):
		t.Fatal("transport Send was not entered")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	select {
	case err := <-callDone:
		if err == nil {
			t.Fatal("Call error = nil, want send cancellation error")
		}
		if !errors.Is(err, context.Canceled) && !errors.Is(err, ErrClosed) {
			t.Fatalf("Call error = %v, want context canceled or ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Call did not return after Close canceled send")
	}
	select {
	case err := <-callback:
		if !errors.Is(err, ErrClosed) && !errors.Is(err, context.Canceled) {
			t.Fatalf("callback error = %v, want ErrClosed or context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("callback did not receive ErrClosed")
	}
	if got := client.callbacks.Len(); got != 0 {
		t.Fatalf("callback cache len = %d, want 0", got)
	}
	if sent := transport.Sent(); len(sent) != 0 {
		t.Fatalf("sent count = %d, want 0", len(sent))
	}
}

func TestCallSendErrorReturnsAndFiresCallback(t *testing.T) {
	transport := &failingSendTransport{testTransport: newTestTransport()}
	client := newTestClient(t, transport, Options{AwaitTimeout: 10 * time.Millisecond})
	if err := client.Start(); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer func() { _ = client.Close() }()

	done := make(chan error, 1)
	err := client.Call(wrapperspb.String("req"), func(_ *wrapperspb.StringValue, err error) {
		done <- err
	})
	if !errors.Is(err, errSendFailed) {
		t.Fatalf("Call error = %v, want send failed", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, errSendFailed) {
			t.Fatalf("callback error = %v, want send failed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("callback did not receive send error")
	}
	if got := client.callbacks.Len(); got != 0 {
		t.Fatalf("callback cache len = %d, want 0", got)
	}
	if sent := transport.Sent(); len(sent) != 0 {
		t.Fatalf("sent count = %d, want 0", len(sent))
	}
}

func TestCallbackQueuePopsExpiredFromFront(t *testing.T) {
	cache := newCallbackCache(time.Nanosecond)
	cache.Add("old", func(rpc.Message, error) error { return nil })
	time.Sleep(time.Millisecond)
	if expired := cache.PopExpired(time.Now()); len(expired) != 1 {
		t.Fatalf("expired = %d, want 1", len(expired))
	}
	if got := cache.Len(); got != 0 {
		t.Fatalf("cache len = %d, want 0", got)
	}
}

type testTransport struct {
	mu      sync.Mutex
	inbound chan rpc.Message
	sent    []rpc.Message
	closed  bool
}

func newTestTransport() *testTransport {
	return &testTransport{inbound: make(chan rpc.Message, 32)}
}

func (t *testTransport) Send(_ context.Context, msg rpc.Message) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	t.sent = append(t.sent, msg.Clone())
	return nil
}

func (t *testTransport) Recv(ctx context.Context) (rpc.Message, error) {
	select {
	case <-ctx.Done():
		return rpc.Message{}, ctx.Err()
	case msg, ok := <-t.inbound:
		if !ok {
			return rpc.Message{}, ErrClosed
		}
		return msg.Clone(), nil
	}
}

func (t *testTransport) Close(context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	close(t.inbound)
	return nil
}

func (t *testTransport) Inject(msg rpc.Message) bool {
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return false
	}
	t.inbound <- msg.Clone()
	return true
}

func (t *testTransport) Sent() []rpc.Message {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]rpc.Message, len(t.sent))
	copy(out, t.sent)
	return out
}

type blockingSendTransport struct {
	*testTransport
	entered   chan struct{}
	release   chan struct{}
	enterOnce sync.Once
}

func newBlockingSendTransport() *blockingSendTransport {
	return &blockingSendTransport{
		testTransport: newTestTransport(),
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
	}
}

func (t *blockingSendTransport) Send(ctx context.Context, msg rpc.Message) error {
	t.enterOnce.Do(func() {
		close(t.entered)
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.release:
		return t.testTransport.Send(ctx, msg)
	}
}

var errSendFailed = errors.New("send failed")

type failingSendTransport struct {
	*testTransport
}

func (t *failingSendTransport) Send(context.Context, rpc.Message) error {
	return errSendFailed
}

type requestResult struct {
	name  string
	value string
	err   error
}

func newTestClient(t *testing.T, transport Transport, opts Options) *Client {
	t.Helper()
	client, err := New(transport, opts)
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	return client
}

func waitForSent(t *testing.T, transport *testTransport, count int) []rpc.Message {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		sent := transport.Sent()
		if len(sent) >= count {
			return sent
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("sent message count < %d", count)
	return nil
}

func newStringMessage(t *testing.T, name string, value string) rpc.Message {
	t.Helper()
	msg, err := rpc.NewMessagePB(name, wrapperspb.String(value))
	if err != nil {
		t.Fatalf("NewMessagePB error = %v", err)
	}
	return msg
}

func hasMetric(events []observe.Event, name string, inbox string) bool {
	for _, event := range events {
		if event.Name == name && event.Dimensions["inbox"] == inbox {
			return true
		}
	}
	return false
}
