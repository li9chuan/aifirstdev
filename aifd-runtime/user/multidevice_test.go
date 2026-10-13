package user

import (
	"bytes"
	"context"
	"net/http/httptest"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	proxynats "github.com/li9chuan/aifirstdev/aifd-proxy/adapters/nats"
	proxybroker "github.com/li9chuan/aifirstdev/aifd-proxy/adapters/nats/broker"
	proxyws "github.com/li9chuan/aifirstdev/aifd-proxy/adapters/websocket"
	codec "github.com/li9chuan/aifirstdev/aifd-proxy/codec"
	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	proxyruntime "github.com/li9chuan/aifirstdev/aifd-proxy/runtime"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	rpcnats "github.com/li9chuan/aifirstdev/aifd-rpc/adapters/nats"
	contracts "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	proxypb "github.com/li9chuan/aifirstdev/aifd-schema/generated/proxy"
	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// Exercise real NATS sockets and two independent WS proxies, including identical
// client inbox numbers. UID events must reach both proxies; replies must stay local.
func TestNATSMultiProxyIdenticalInboxesAndUIDBroadcast(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	broker, err := proxybroker.Start(ctx, proxybroker.Config{Options: &natsserver.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true}, ReadyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close(context.Background())
	newBus := func(name string) *rpcnats.Bus {
		bus, err := rpcnats.NewBus(rpcnats.Config{Options: natsgo.Options{Servers: []string{broker.ClientURL()}, Name: name, Timeout: time.Second}, DefaultTimeout: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = bus.Close(context.Background()) })
		return bus
	}
	ownerBus := newBus("owner")
	db := rdb.MustNewMemoryDB()
	defer db.Close(context.Background())
	rt := NewRuntime(rpc.NewRegistry(), newInt64StoreWithDB(t, db), nil, WithBus(ownerBus))
	defer rt.Close(context.Background())
	if _, err := rt.CreateUser(ctx, 42); err != nil {
		t.Fatal(err)
	}
	if err := rt.RegisterRPC(); err != nil {
		t.Fatal(err)
	}
	if err := rt.Client("MceEcho", func(s *rpc.Session, _ *User[*wrapperspb.Int64Value], req *wrapperspb.StringValue) {
		if req.Value == "deny" {
			s.Error("FORBIDDEN", "denied")
			return
		}
		s.Response(wrapperspb.String(req.Value))
		s.Push(wrapperspb.Int64(42))
	}); err != nil {
		t.Fatal(err)
	}
	if err := rpc.Start(ctx, ownerBus, rt.Registry()); err != nil {
		t.Fatal(err)
	}
	if err := ownerBus.NatsConn().Flush(); err != nil {
		t.Fatal(err)
	}
	newProxy := func(name string) *coderws.Conn {
		bus := newBus(name)
		delivery := proxyruntime.NewDelivery()
		gateway := proxyruntime.NewGateway(nil, proxyruntime.NewRPCUpstream(bus, delivery))
		downlink := proxynats.NewDownlink(bus.NatsConn(), gateway, delivery, proxynats.Config{})
		ready, _ := contracts.NewMessagePB("MseReady", wrapperspb.String("ready"))
		handler := proxyws.NewServer(proxyws.Config{Bus: bus, Gateway: gateway, Delivery: delivery, Downlink: downlink, DuplicatePolicy: proxyws.DuplicateAllow,
			HandshakeHandler: proxy.HandshakeHandlerFunc(func(context.Context, proxy.AuthRequest) (proxy.HandshakeResult, error) {
				return proxy.HandshakeResult{Handled: true, Identity: proxy.Identity{UID: 42}, InitialMessage: &ready}, nil
			}),
		})
		server := httptest.NewServer(handler)
		t.Cleanup(func() { _ = handler.Close(context.Background()); server.Close(); _ = downlink.Close() })
		ws, _, err := coderws.Dial(ctx, "ws"+server.URL[4:], nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ws.Close(coderws.StatusNormalClosure, "done") })
		head, _ := readWire(t, ctx, ws)
		if head.Topic != "MseReady" || head.Inbox != 0 {
			t.Fatalf("ready=%v", head)
		}
		return ws
	}
	first, second := newProxy("proxy-a"), newProxy("proxy-b")
	if rt.OnlineConnections(42) != 2 {
		t.Fatal("proxy boots share one lease")
	}
	writeWire(t, ctx, first, "A")
	writeWire(t, ctx, second, "B")
	collect := func(ws *coderws.Conn, want string, events int) {
		replied := false
		for !replied || events > 0 {
			head, body := readWire(t, ctx, ws)
			if head.Inbox == 9 {
				value := new(wrapperspb.StringValue)
				if head.PbType != "StringValue" || proto.Unmarshal(body, value) != nil || value.Value != want || replied {
					t.Fatalf("reply crossed proxies: head=%v want=%s", head, want)
				}
				replied = true
			} else {
				value := new(wrapperspb.Int64Value)
				if head.Inbox != 0 || head.PbType != "Int64Value" || proto.Unmarshal(body, value) != nil || value.Value != 42 {
					t.Fatalf("invalid UID event: %v", head)
				}
				events--
			}
		}
	}
	collect(first, "A", 2)
	collect(second, "B", 2)
	writeWire(t, ctx, first, "deny")
	head, body := readWire(t, ctx, first)
	denied := new(proxypb.RPCError)
	if head.Inbox != 9 || head.PbType != "RPCError" || proto.Unmarshal(body, denied) != nil || denied.Code != "FORBIDDEN" {
		t.Fatalf("error reply=%v", head)
	}
	// A request error must not leak into B's inbox, even when its next request reuses 9.
	writeWire(t, ctx, second, "B-again")
	collect(second, "B-again", 1)
	_ = first.Close(coderws.StatusNormalClosure, "done")
	deadline := time.Now().Add(time.Second)
	for rt.OnlineConnections(42) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if rt.OnlineConnections(42) != 1 {
		t.Fatal("disconnect removed wrong lease")
	}
	writeWire(t, ctx, second, "still-live")
	collect(second, "still-live", 1)
}

func writeWire(t *testing.T, ctx context.Context, ws *coderws.Conn, value string) {
	t.Helper()
	body, _ := proto.Marshal(wrapperspb.String(value))
	parts, err := codec.EncodePacket(&proxypb.MsgHead{Topic: "MceEcho", PbType: "StringValue", Inbox: 9}, body)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.Write(ctx, coderws.MessageBinary, bytes.Join(parts, nil)); err != nil {
		t.Fatal(err)
	}
}

func readWire(t *testing.T, ctx context.Context, ws *coderws.Conn) (*proxypb.MsgHead, []byte) {
	t.Helper()
	_, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	head, body, err := codec.DecodePacket(data)
	if err != nil {
		t.Fatal(err)
	}
	return head, body
}
