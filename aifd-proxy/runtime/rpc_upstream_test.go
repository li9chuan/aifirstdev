package runtime_test

import (
	"context"
	proxyruntime "github.com/li9chuan/aifirstdev/aifd-proxy/runtime"
	"testing"
	"time"

	proxylocal "github.com/li9chuan/aifirstdev/aifd-proxy/adapters/local"
	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	rpclocal "github.com/li9chuan/aifirstdev/aifd-rpc/adapters/local"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

func TestRPCUpstreamForwardsEnvelopeToBus(t *testing.T) {
	ctx := context.Background()
	bus := rpclocal.NewBus(rpclocal.Config{ServiceName: "proxy-upstream-test", Buffer: 4})
	defer bus.Close(ctx)
	if err := bus.Register(rpc.Route{
		Name:    "McePing",
		Kind:    rpc.RouteKindClient,
		Handler: func(context.Context, rpc.Session, rpc.Message) error { return nil },
	}); err != nil {
		t.Fatalf("Register error = %v", err)
	}

	upstream := proxyruntime.NewRPCUpstream(bus, proxyruntime.NewDelivery())
	if err := upstream.Forward(ctx, proxy.Envelope{UID: 1001, Message: rpc.Message{Name: "McePing", Payload: []byte("ping")}}); err != nil {
		t.Fatalf("Forward error = %v", err)
	}

	select {
	case delivery := <-bus.Deliveries():
		if delivery.Session().UID() != 1001 || delivery.Message().Name != "McePing" {
			t.Fatalf("delivery = uid:%d msg:%s", delivery.Session().UID(), delivery.Message().Name)
		}
		if err := delivery.Handle(ctx); err != nil {
			t.Fatalf("delivery Handle error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("delivery timeout")
	}
}

func TestMissingRouteErrorKeepsOriginalConnectionInboxAndTrace(t *testing.T) {
	ctx := context.Background()
	bus := rpclocal.NewBus(rpclocal.Config{})
	defer bus.Close(ctx)
	var pushes []rpc.Message
	unsubscribe, err := bus.SubscribePush(42, func(_ context.Context, msg rpc.Message) error {
		pushes = append(pushes, msg)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	delivery := proxyruntime.NewDelivery()
	a, b := proxylocal.NewConnection("a"), proxylocal.NewConnection("b")
	_ = delivery.Add(a)
	_ = delivery.Add(b)
	upstream := proxyruntime.NewRPCUpstream(bus, delivery)
	if err := upstream.Forward(ctx, proxy.Envelope{UID: 42, ConnectionID: "a", Message: rpc.Message{Name: "MceMissing", Header: map[string]string{rpc.HeaderClientInbox: "9", rpc.HeaderTraceID: "request-trace"}}}); err != nil {
		t.Fatal(err)
	}
	if a.Closed() || b.Closed() || len(a.Sent()) != 1 || len(b.Sent()) != 0 || len(pushes) != 0 {
		t.Fatal("missing-route response escaped or closed a connection")
	}
	rsp := a.Sent()[0]
	if rsp.Name != "RPCError" || rsp.Header[rpc.HeaderStatus] != "UNSUPPORTED" || rsp.Header[rpc.HeaderClientInbox] != "9" || rsp.Header[rpc.HeaderTraceID] != "request-trace" {
		t.Fatalf("missing-route response: %+v", rsp)
	}
}

func TestRPCRepliesWithEqualClientInboxesStayOnOriginalConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	bus := rpclocal.NewBus(rpclocal.Config{Buffer: 8})
	defer func() { cancel(); _ = bus.Close(context.Background()) }()
	pushes := make(chan rpc.Message, 1)
	unsubscribe, err := bus.SubscribePush(42, func(_ context.Context, msg rpc.Message) error {
		pushes <- msg
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	if err := bus.Register(rpc.Route{Name: "MceEcho", Kind: rpc.RouteKindClient, Handler: func(ctx context.Context, s rpc.Session, msg rpc.Message) error {
		return s.Respond(ctx, rpc.Message{Name: "MseEcho", Payload: append([]byte("ack:"), msg.Payload...)})
	}}); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case d := <-bus.Deliveries():
				if d == nil {
					return
				}
				_ = d.Handle(ctx)
			}
		}
	}()
	delivery := proxyruntime.NewDelivery()
	a, b := proxylocal.NewConnection("a"), proxylocal.NewConnection("b")
	if err := delivery.Add(a); err != nil {
		t.Fatal(err)
	}
	if err := delivery.Add(b); err != nil {
		t.Fatal(err)
	}
	upstream := proxyruntime.NewRPCUpstream(bus, delivery)
	for _, request := range []struct{ id, body string }{{"a", "first"}, {"b", "second"}} {
		if err := upstream.Forward(ctx, proxy.Envelope{UID: 42, ConnectionID: request.id, Message: rpc.Message{Name: "MceEcho", Payload: []byte(request.body), Header: map[string]string{rpc.HeaderClientInbox: "7"}}}); err != nil {
			t.Fatal(err)
		}
	}
	if len(a.Sent()) != 1 || len(b.Sent()) != 1 || string(a.Sent()[0].Payload) != "ack:first" || string(b.Sent()[0].Payload) != "ack:second" {
		t.Fatalf("mixed replies: %v %v", a.Sent(), b.Sent())
	}
	if a.Sent()[0].Header[rpc.HeaderClientInbox] != "7" || b.Sent()[0].Header[rpc.HeaderClientInbox] != "7" {
		t.Fatal("original callback inbox lost")
	}
	select {
	case <-pushes:
		t.Fatal("request reply escaped into UID broadcast")
	default:
	}
	if err := bus.Push(ctx, 42, rpc.Message{Name: "MseChanged", Header: map[string]string{rpc.HeaderClientInbox: "7"}}); err != nil {
		t.Fatal(err)
	}
	if (<-pushes).Header[rpc.HeaderClientInbox] != "" {
		t.Fatal("UID event retained request inbox")
	}
}
