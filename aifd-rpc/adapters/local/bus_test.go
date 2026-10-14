package local

import (
	"context"
	"errors"
	"testing"
	"time"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	rpcruntime "github.com/li9chuan/aifirstdev/aifd-rpc/runtime"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func startTestRunner(ctx context.Context, bus *Bus) {
	go rpcruntime.Run(ctx, bus.Deliveries(), rpcruntime.RunnerOptions{Workers: 2, QueueSize: 8})
}

func TestBusTellAndCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := NewBus(Config{ServiceName: "test", Buffer: 8})
	defer bus.Close(context.Background())
	startTestRunner(ctx, bus)

	called := make(chan int64, 1)
	if err := bus.Register(rpc.Route{
		Name: "Echo",
		Handler: func(ctx context.Context, session rpc.Session, msg rpc.Message) error {
			called <- session.UID()
			return session.Respond(ctx, msg)
		},
	}); err != nil {
		t.Fatalf("Register error = %v", err)
	}

	if err := bus.Tell(ctx, 7, rpc.Message{Name: "Echo", Payload: []byte("tell")}); err != nil {
		t.Fatalf("Tell error = %v", err)
	}
	select {
	case uid := <-called:
		if uid != 7 {
			t.Fatalf("uid = %d, want 7", uid)
		}
	case <-time.After(time.Second):
		t.Fatal("Tell handler not called")
	}

	rspCh := make(chan rpc.Message, 1)
	if err := bus.Call(ctx, 9, rpc.Message{Name: "Echo", Payload: []byte("call")}, func(_ context.Context, _ rpc.Session, msg rpc.Message) error {
		rspCh <- msg
		return nil
	}, rpc.CallOptions{Timeout: time.Second}); err != nil {
		t.Fatalf("Call error = %v", err)
	}
	<-called
	select {
	case rsp := <-rspCh:
		if string(rsp.Payload) != "call" {
			t.Fatalf("rsp payload = %s, want call", string(rsp.Payload))
		}
	case <-time.After(time.Second):
		t.Fatal("Call response not received")
	}
}

func TestBusRequestPBAndPush(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := NewBus(Config{ServiceName: "test", Buffer: 8})
	defer bus.Close(context.Background())
	startTestRunner(ctx, bus)

	if err := bus.Register(rpc.Route{
		Name: "PB",
		Handler: func(ctx context.Context, session rpc.Session, msg rpc.Message) error {
			var req wrapperspb.StringValue
			if err := msg.DecodePB(&req); err != nil {
				return err
			}
			return session.RespondPB(ctx, wrapperspb.String(req.Value+":rsp"))
		},
	}); err != nil {
		t.Fatalf("Register error = %v", err)
	}

	var rsp wrapperspb.StringValue
	if err := bus.RequestPB(ctx, 1, "PB", wrapperspb.String("hello"), &rsp, rpc.RequestOptions{Timeout: time.Second}); err != nil {
		t.Fatalf("RequestPB error = %v", err)
	}
	if rsp.Value != "hello:rsp" {
		t.Fatalf("rsp = %s, want hello:rsp", rsp.Value)
	}

	var pushes []rpc.Message
	unsubscribe, err := bus.SubscribePush(10, func(_ context.Context, msg rpc.Message) error {
		pushes = append(pushes, msg)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	if err := bus.PushPB(ctx, 10, wrapperspb.String("push")); err != nil {
		t.Fatalf("PushPB error = %v", err)
	}
	if len(pushes) != 1 {
		t.Fatalf("unexpected pushes %+v", pushes)
	}
	var pushed wrapperspb.StringValue
	if err := pushes[0].DecodePB(&pushed); err != nil || pushed.Value != "push" {
		t.Fatalf("push = %q, error = %v", pushed.Value, err)
	}
}

func TestBusRequestPBReturnsStatusError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := NewBus(Config{ServiceName: "test", Buffer: 8})
	defer bus.Close(context.Background())
	startTestRunner(ctx, bus)

	if err := bus.Register(rpc.Route{
		Name: "PBError",
		Handler: func(ctx context.Context, session rpc.Session, _ rpc.Message) error {
			return session.Respond(ctx, rpc.Message{
				Name:    "RPCError",
				Payload: []byte("not protobuf"),
				Header: map[string]string{
					rpc.HeaderStatus:       "INVALID",
					rpc.HeaderErrorMessage: "bad request",
				},
			})
		},
	}); err != nil {
		t.Fatalf("Register error = %v", err)
	}

	var rsp wrapperspb.StringValue
	err := bus.RequestPB(ctx, 1, "PBError", wrapperspb.String("hello"), &rsp, rpc.RequestOptions{Timeout: time.Second})
	var statusErr *rpc.StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("RequestPB error = %v, want StatusError", err)
	}
	if statusErr.Code != "INVALID" || statusErr.Message != "bad request" {
		t.Fatalf("status error = %+v, want INVALID bad request", statusErr)
	}
	if rsp.Value != "" {
		t.Fatalf("response decoded value = %q, want empty", rsp.Value)
	}
}

func TestBusStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := NewBus(Config{ServiceName: "test", Buffer: 8})
	defer bus.Close(context.Background())
	startTestRunner(ctx, bus)

	done := make(chan struct{}, 1)
	if err := bus.RegisterStream(ctx, rpc.Route{
		Name: "stream.reward",
		Handler: func(context.Context, rpc.Session, rpc.Message) error {
			done <- struct{}{}
			return nil
		},
	}); err != nil {
		t.Fatalf("RegisterStream error = %v", err)
	}
	if err := bus.SaveToStream(ctx, 1, rpc.Message{Name: "stream.reward"}); err != nil {
		t.Fatalf("SaveToStream error = %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream handler not called")
	}
}

func TestCloseWaitsForInFlightDelivery(t *testing.T) {
	ctx := context.Background()
	bus := NewBus(Config{ServiceName: "test", Buffer: 8})
	startTestRunner(ctx, bus)

	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	if err := bus.Register(rpc.Route{
		Name: "Slow",
		Handler: func(context.Context, rpc.Session, rpc.Message) error {
			close(started)
			<-release
			close(done)
			return nil
		},
	}); err != nil {
		t.Fatalf("Register error = %v", err)
	}
	if err := bus.Tell(ctx, 1, rpc.Message{Name: "Slow"}); err != nil {
		t.Fatalf("Tell error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}

	closed := make(chan error, 1)
	go func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		closed <- bus.Close(closeCtx)
	}()
	select {
	case err := <-closed:
		t.Fatalf("Close returned before handler finished: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not finish")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not return after handler finished")
	}
}
