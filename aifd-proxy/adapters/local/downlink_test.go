package local

import (
	"context"
	"encoding/binary"
	"testing"

	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	proxyruntime "github.com/li9chuan/aifirstdev/aifd-proxy/runtime"
	localrpc "github.com/li9chuan/aifirstdev/aifd-rpc/adapters/local"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

type downlinkTestConnection struct {
	id       string
	messages []rpc.Message
}

func (c *downlinkTestConnection) ID() string { return c.id }
func (c *downlinkTestConnection) Send(_ context.Context, msg rpc.Message) error {
	c.messages = append(c.messages, msg)
	return nil
}
func (c *downlinkTestConnection) Close(context.Context) error { return nil }

func TestLocalPubSubReachesEveryProxyAndUnsubscribesIndependently(t *testing.T) {
	ctx := context.Background()
	bus := localrpc.NewBus(localrpc.Config{})
	defer bus.Close(ctx)
	_ = bus.Push(ctx, 42, rpc.Message{Name: "old"})
	connect := func(id string) (*Downlink, *downlinkTestConnection) {
		gateway := proxyruntime.NewGateway(nil, nil)
		delivery := proxyruntime.NewDelivery()
		conn := &downlinkTestConnection{id: id}
		if err := delivery.Add(conn); err != nil {
			t.Fatal(err)
		}
		if _, err := gateway.AcceptIdentity(conn, proxy.Identity{UID: 42}); err != nil {
			t.Fatal(err)
		}
		downlink := NewDownlink(bus, gateway, delivery)
		if err := downlink.SubscribeUID(42); err != nil {
			t.Fatal(err)
		}
		if err := downlink.SubscribeUID(42); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = downlink.Close() })
		return downlink, conn
	}
	first, a := connect("A")
	second, b := connect("B")
	msg := rpc.Message{Name: "event", Payload: []byte{1}, Header: map[string]string{rpc.HeaderClientInbox: "9"}}
	if err := bus.Push(ctx, 42, msg); err != nil {
		t.Fatal(err)
	}
	for _, conn := range []*downlinkTestConnection{a, b} {
		if len(conn.messages) != 1 || conn.messages[0].Name != "event" || conn.messages[0].Header[rpc.HeaderClientInbox] != "" {
			t.Fatalf("fanout=%v", conn.messages)
		}
	}
	a.messages[0].Payload[0] = 99
	if b.messages[0].Payload[0] != 1 || msg.Payload[0] != 1 {
		t.Fatal("subscribers share mutable payload")
	}
	if err := first.UnsubscribeUID(42); err != nil {
		t.Fatal(err)
	}
	if err := bus.Push(ctx, 42, msg); err != nil {
		t.Fatal(err)
	}
	if len(a.messages) != 1 || len(b.messages) != 2 {
		t.Fatal("unsubscribing one proxy removed its peer")
	}
	if err := bus.Push(ctx, 43, msg); err != nil {
		t.Fatal(err)
	}
	if len(b.messages) != 2 {
		t.Fatal("another UID received an event")
	}
	for i := range 1000 {
		payload := make([]byte, 4)
		binary.LittleEndian.PutUint32(payload, uint32(i))
		if err := bus.Push(ctx, 42, rpc.Message{Name: "sequence", Payload: payload, Header: msg.Header}); err != nil {
			t.Fatal(err)
		}
	}
	if len(a.messages) != 1 || len(b.messages) != 1002 {
		t.Fatalf("continuous fanout: a=%d b=%d", len(a.messages), len(b.messages))
	}
	for i, received := range b.messages[2:] {
		if received.Name != "sequence" || len(received.Payload) != 4 || binary.LittleEndian.Uint32(received.Payload) != uint32(i) || received.Header[rpc.HeaderClientInbox] != "" {
			t.Fatalf("event %d: %+v", i, received)
		}
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bus.Push(ctx, 42, msg); err != nil {
		t.Fatal(err)
	}
	if len(b.messages) != 1002 {
		t.Fatal("closed downlink received an event")
	}
}
