package websocket

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	coderws "github.com/coder/websocket"
	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	proxyruntime "github.com/li9chuan/aifirstdev/aifd-proxy/runtime"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	proxypb "github.com/li9chuan/aifirstdev/aifd-schema/generated/proxy"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestInvalidHandshakeCredentialsRejectBeforeUpgradeWithoutFallback(t *testing.T) {
	called := false
	handler := NewServer(Config{
		Gateway:  proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(context.Context, proxy.Envelope) error { return nil })),
		Delivery: proxyruntime.NewDelivery(),
		HandshakeHandler: proxy.HandshakeHandlerFunc(func(context.Context, proxy.AuthRequest) (proxy.HandshakeResult, error) {
			return proxy.HandshakeResult{Handled: true}, errors.New("invalid token")
		}),
		LoginHandler: proxy.LoginHandlerFunc(func(context.Context, proxy.LoginRequest) (proxy.LoginResult, error) {
			called = true
			return proxy.LoginResult{}, nil
		}),
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized || called {
		t.Fatalf("status=%d fallback=%v", rec.Code, called)
	}
}

func TestHandshakeActivationFailureRollsBackMembershipAndSubscription(t *testing.T) {
	gateway := proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(context.Context, proxy.Envelope) error { return nil }))
	downlink := &recordingDownlink{}
	closed := make(chan proxypb.SessionEndReason, 1)
	handler := NewServer(Config{Gateway: gateway, Delivery: proxyruntime.NewDelivery(), Downlink: downlink,
		HandshakeHandler: proxy.HandshakeHandlerFunc(func(context.Context, proxy.AuthRequest) (proxy.HandshakeResult, error) {
			return proxy.HandshakeResult{Handled: true, Identity: proxy.Identity{UID: 42}}, nil
		}),
		Lifecycle: SessionLifecycleFuncs{Connected: func(context.Context, proxyruntime.Session) error { return errors.New("owner unavailable") }, Disconnected: func(_ context.Context, _ proxyruntime.Session, reason proxypb.SessionEndReason) error {
			closed <- reason
			return nil
		}},
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	ws := dial(t, server)
	defer ws.Close(coderws.StatusNormalClosure, "done")
	expectReadError(t, ws)
	eventually(t, func() bool { return len(gateway.ConnectionIDs(42)) == 0 && len(downlink.unsubscriptions()) == 1 })
	select {
	case reason := <-closed:
		if reason != proxypb.SessionEndReason_SESSION_END_REASON_DISCONNECTED {
			t.Fatal(reason)
		}
	default:
		t.Fatal("lease cleanup missing")
	}
}

func TestHandshakeMultiDeviceKeepsSubscriptionUntilLastConnection(t *testing.T) {
	gateway := proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(context.Context, proxy.Envelope) error { return nil }))
	downlink := &recordingDownlink{}
	ready, _ := rpc.NewMessagePB("MseReady", wrapperspb.String("ready"))
	ready.Header[rpc.HeaderClientInbox] = "7"
	handler := NewServer(Config{Gateway: gateway, Delivery: proxyruntime.NewDelivery(), Downlink: downlink, DuplicatePolicy: DuplicateAllow,
		HandshakeHandler: proxy.HandshakeHandlerFunc(func(context.Context, proxy.AuthRequest) (proxy.HandshakeResult, error) {
			return proxy.HandshakeResult{Handled: true, Identity: proxy.Identity{UID: 42}, InitialMessage: &ready}, nil
		}),
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	defer handler.Close(context.Background())
	first, second := dial(t, server), dial(t, server)
	defer second.Close(coderws.StatusNormalClosure, "done")
	for _, ws := range []*coderws.Conn{first, second} {
		head, _ := readPacket(t, ws)
		if head.Inbox != 0 || head.Topic != "MseReady" {
			t.Fatalf("ready=%v", head)
		}
	}
	if len(gateway.ConnectionIDs(42)) != 2 || len(downlink.subscriptions()) != 1 {
		t.Fatal("subscription duplicated")
	}
	_ = first.Close(coderws.StatusNormalClosure, "done")
	eventually(t, func() bool { return len(gateway.ConnectionIDs(42)) == 1 })
	if len(downlink.unsubscriptions()) != 0 {
		t.Fatal("first device removed shared subscription")
	}
	_ = second.Close(coderws.StatusNormalClosure, "done")
	eventually(t, func() bool { return len(gateway.ConnectionIDs(42)) == 0 && len(downlink.unsubscriptions()) == 1 })
}
