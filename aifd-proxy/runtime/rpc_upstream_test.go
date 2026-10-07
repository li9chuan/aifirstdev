package runtime

import (
	"context"
	"testing"
	"time"

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

	upstream := NewRPCUpstream(bus)
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
