package websocket

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	proxycodec "github.com/li9chuan/aifirstdev/aifd-proxy/codec"
	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	proxyruntime "github.com/li9chuan/aifirstdev/aifd-proxy/runtime"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	proxypb "github.com/li9chuan/aifirstdev/aifd-schema/generated/proxy"
	coderws "github.com/coder/websocket"
)

func TestServerUsesLoginHandlerTopicWhenConfigEmpty(t *testing.T) {
	handler := &topicLoginHandler{
		topic: "MceAuth",
		login: func(_ context.Context, req proxy.LoginRequest) (proxy.LoginResult, error) {
			if req.Message.Name != "MceAuth" {
				t.Fatalf("login message name = %q, want MceAuth", req.Message.Name)
			}
			return proxy.LoginResult{
				Identity: proxy.Identity{UID: 1001, Account: "account-1"},
				Response: &rpc.Message{
					Name:    "MseAuth",
					Payload: []byte{1},
					Header:  map[string]string{rpc.HeaderPBType: "StringValue"},
				},
			}, nil
		},
	}
	server := httptest.NewServer(NewServer(Config{
		Gateway:      proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(context.Context, proxy.Envelope) error { return nil })),
		Delivery:     proxyruntime.NewDelivery(),
		LoginHandler: handler,
	}))
	defer server.Close()

	ws := dial(t, server)
	defer ws.Close(coderws.StatusNormalClosure, "done")
	writePacket(t, ws, "MceAuth", "StringValue", 0, []byte{1})
	head, _ := readPacket(t, ws)
	if head.Topic != "MseAuth" {
		t.Fatalf("login response topic = %q, want MseAuth", head.Topic)
	}
}

func TestServerValidateRejectsMismatchedLoginTopic(t *testing.T) {
	err := NewServer(Config{
		LoginTopic:   "MceOther",
		Gateway:      proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(context.Context, proxy.Envelope) error { return nil })),
		Delivery:     proxyruntime.NewDelivery(),
		LoginHandler: &topicLoginHandler{topic: "MceAuth"},
	}).Validate()
	if !errors.Is(err, ErrConfigMissing) {
		t.Fatalf("Validate error = %v, want ErrConfigMissing", err)
	}
}

func TestServerListenAndServeRejectsInvalidConfig(t *testing.T) {
	err := NewServer(Config{
		LoginTopic:   "Game.AuthOK",
		Gateway:      proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(context.Context, proxy.Envelope) error { return nil })),
		Delivery:     proxyruntime.NewDelivery(),
		LoginHandler: proxy.LoginHandlerFunc(func(context.Context, proxy.LoginRequest) (proxy.LoginResult, error) { return proxy.LoginResult{}, nil }),
	}).ListenAndServe(context.Background())
	if !errors.Is(err, proxycodec.ErrInvalidPacket) {
		t.Fatalf("ListenAndServe error = %v, want invalid packet", err)
	}
}

func TestServerServeHTTPRejectsInvalidConfig(t *testing.T) {
	server := NewServer(Config{
		LoginTopic:   "Game.AuthOK",
		Gateway:      proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(context.Context, proxy.Envelope) error { return nil })),
		Delivery:     proxyruntime.NewDelivery(),
		LoginHandler: proxy.LoginHandlerFunc(func(context.Context, proxy.LoginRequest) (proxy.LoginResult, error) { return proxy.LoginResult{}, nil }),
	})
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestServerLoginThenForward(t *testing.T) {
	envCh := make(chan proxy.Envelope, 1)
	gateway := proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(_ context.Context, env proxy.Envelope) error {
		envCh <- env
		return nil
	}))
	delivery := proxyruntime.NewDelivery()
	downlink := &recordingDownlink{}
	server := httptest.NewServer(NewServer(Config{
		Gateway:  gateway,
		Delivery: delivery,
		Downlink: downlink,
		LoginHandler: proxy.LoginHandlerFunc(func(_ context.Context, req proxy.LoginRequest) (proxy.LoginResult, error) {
			if req.Message.Name != "MceLogin" {
				t.Fatalf("login message name = %q, want MceLogin", req.Message.Name)
			}
			if req.Message.Header[rpc.HeaderPBType] != "Int64Value" {
				t.Fatalf("login pbType = %q, want Int64Value", req.Message.Header[rpc.HeaderPBType])
			}
			return proxy.LoginResult{
				Identity: proxy.Identity{UID: 1001, Account: "account-1"},
				Response: &rpc.Message{
					Name:    "MseLogin",
					Payload: []byte{9},
					Header:  map[string]string{rpc.HeaderPBType: "StringValue"},
				},
			}, nil
		}),
	}))
	defer server.Close()

	ws := dial(t, server)
	defer ws.Close(coderws.StatusNormalClosure, "done")
	writePacket(t, ws, "MceLogin", "Int64Value", 0, []byte{1})
	head, body := readPacket(t, ws)
	if head.Topic != "MseLogin" || head.PbType != "StringValue" || !bytes.Equal(body, []byte{9}) {
		t.Fatalf("login response = head:%+v body:%v", head, body)
	}
	if got := downlink.subscriptions(); len(got) != 1 || got[0] != 1001 {
		t.Fatalf("subscriptions = %v, want [1001]", got)
	}

	writePacket(t, ws, "MceInventoryInfo", "StringValue", 123, []byte("ping"))
	select {
	case env := <-envCh:
		if env.UID != 1001 || env.Message.Name != "MceInventoryInfo" || env.Message.Header[rpc.HeaderPBType] != "StringValue" {
			t.Fatalf("envelope = %+v", env)
		}
		if env.Message.Header[rpc.HeaderClientInbox] != "123" {
			t.Fatalf("client inbox = %q, want 123", env.Message.Header[rpc.HeaderClientInbox])
		}
	case <-time.After(time.Second):
		t.Fatal("forward timeout")
	}
}

func TestServerRejectsPingBeforeLogin(t *testing.T) {
	server := httptest.NewServer(NewServer(Config{
		Gateway: proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(context.Context, proxy.Envelope) error {
			t.Fatal("ping should not enter upstream")
			return nil
		})),
		Delivery:     proxyruntime.NewDelivery(),
		LoginHandler: proxy.LoginHandlerFunc(func(context.Context, proxy.LoginRequest) (proxy.LoginResult, error) { return proxy.LoginResult{}, nil }),
	}))
	defer server.Close()

	ws := dial(t, server)
	defer ws.Close(coderws.StatusNormalClosure, "done")
	writePacket(t, ws, "McePing", "", 0, nil)
	expectReadError(t, ws)
}

func TestServerPingAfterLogin(t *testing.T) {
	_, server := duplicateServer(t, DuplicateKickOld, nil)
	defer server.Close()
	ws := loginConn(t, server)
	defer ws.Close(coderws.StatusNormalClosure, "done")

	writePacket(t, ws, "McePing", "", 0, nil)
	head, body := readPacket(t, ws)
	if head.Topic != "MsePong" || len(body) != 0 {
		t.Fatalf("pong = head:%+v body:%v", head, body)
	}
}

func TestServerRejectsInvalidTopicBeforeLogin(t *testing.T) {
	server := httptest.NewServer(NewServer(Config{
		Gateway: proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(context.Context, proxy.Envelope) error {
			t.Fatal("invalid topic should not enter upstream")
			return nil
		})),
		Delivery:     proxyruntime.NewDelivery(),
		LoginHandler: proxy.LoginHandlerFunc(func(context.Context, proxy.LoginRequest) (proxy.LoginResult, error) { return proxy.LoginResult{}, nil }),
	}))
	defer server.Close()

	ws := dial(t, server)
	defer ws.Close(coderws.StatusNormalClosure, "done")
	writePacket(t, ws, "Game.AuthOK", "StringValue", 0, []byte{1})
	expectReadError(t, ws)
}

func TestServerRejectsNonLoginBeforeLogin(t *testing.T) {
	server := httptest.NewServer(NewServer(Config{
		Gateway:      proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(context.Context, proxy.Envelope) error { return nil })),
		Delivery:     proxyruntime.NewDelivery(),
		LoginHandler: proxy.LoginHandlerFunc(func(context.Context, proxy.LoginRequest) (proxy.LoginResult, error) { return proxy.LoginResult{}, nil }),
	}))
	defer server.Close()

	ws := dial(t, server)
	defer ws.Close(coderws.StatusNormalClosure, "done")
	writePacket(t, ws, "MceInventoryInfo", "StringValue", 0, []byte{1})
	expectReadError(t, ws)
}

func TestServerPublicRequestBeforeLogin(t *testing.T) {
	gateway := proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(context.Context, proxy.Envelope) error {
		t.Fatal("public request should not enter authenticated gateway")
		return nil
	}))
	server := httptest.NewServer(NewServer(Config{
		Gateway:      gateway,
		Delivery:     proxyruntime.NewDelivery(),
		PublicTopics: []string{"McdBootstrap"},
		PublicHandler: proxy.PublicHandlerFunc(func(_ context.Context, req proxy.PublicRequest) (proxy.PublicResult, error) {
			if req.Message.Name != "McdBootstrap" {
				t.Fatalf("public topic = %q, want McdBootstrap", req.Message.Name)
			}
			return proxy.PublicResult{Response: &rpc.Message{
				Name:    "MsdBootstrap",
				Payload: []byte("public"),
				Header:  map[string]string{rpc.HeaderPBType: "StringValue"},
			}}, nil
		}),
		LoginHandler: proxy.LoginHandlerFunc(func(context.Context, proxy.LoginRequest) (proxy.LoginResult, error) {
			return proxy.LoginResult{
				Identity: proxy.Identity{UID: 1001, Account: "account-1"},
				Response: &rpc.Message{
					Name:    "MseLogin",
					Payload: []byte{1},
					Header:  map[string]string{rpc.HeaderPBType: "StringValue"},
				},
			}, nil
		}),
	}))
	defer server.Close()

	ws := dial(t, server)
	defer ws.Close(coderws.StatusNormalClosure, "done")
	writePacket(t, ws, "McdBootstrap", "StringValue", 77, []byte("request"))
	head, body := readPacket(t, ws)
	if head.Topic != "MsdBootstrap" || head.Inbox != 77 || head.PbType != "StringValue" || !bytes.Equal(body, []byte("public")) {
		t.Fatalf("public response = head:%+v body:%v", head, body)
	}
	if ids := gateway.ConnectionIDs(1001); len(ids) != 0 {
		t.Fatalf("public request created session ids = %v", ids)
	}

	writePacket(t, ws, "MceLogin", "Int64Value", 88, []byte{1})
	loginHead, _ := readPacket(t, ws)
	if loginHead.Topic != "MseLogin" || loginHead.Inbox != 88 {
		t.Fatalf("login after public response = %+v, want topic MseLogin inbox 88", loginHead)
	}
}

func TestServerPublicTopicsRequireHandler(t *testing.T) {
	err := NewServer(Config{
		Gateway:      proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(context.Context, proxy.Envelope) error { return nil })),
		Delivery:     proxyruntime.NewDelivery(),
		PublicTopics: []string{"McdBootstrap"},
		LoginHandler: proxy.LoginHandlerFunc(func(context.Context, proxy.LoginRequest) (proxy.LoginResult, error) { return proxy.LoginResult{}, nil }),
	}).Validate()
	if !errors.Is(err, ErrConfigMissing) {
		t.Fatalf("Validate error = %v, want ErrConfigMissing", err)
	}
}

func TestServerRejectsInvalidTopicAfterLogin(t *testing.T) {
	envCh := make(chan proxy.Envelope, 1)
	server := httptest.NewServer(NewServer(Config{
		Gateway: proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(_ context.Context, env proxy.Envelope) error {
			envCh <- env
			return nil
		})),
		Delivery: proxyruntime.NewDelivery(),
		LoginHandler: proxy.LoginHandlerFunc(func(context.Context, proxy.LoginRequest) (proxy.LoginResult, error) {
			return proxy.LoginResult{
				Identity: proxy.Identity{UID: 1001, Account: "account-1"},
				Response: &rpc.Message{
					Name:    "MseLogin",
					Payload: []byte{1},
					Header:  map[string]string{rpc.HeaderPBType: "StringValue"},
				},
			}, nil
		}),
	}))
	defer server.Close()

	ws := loginConn(t, server)
	defer ws.Close(coderws.StatusNormalClosure, "done")
	writePacket(t, ws, "Game.AuthOK", "StringValue", 0, []byte{1})
	expectReadError(t, ws)
	select {
	case env := <-envCh:
		t.Fatalf("invalid topic entered upstream: %+v", env)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestServerAllowsDownlinkTopicsWithoutMsPrefix(t *testing.T) {
	gateway := proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(context.Context, proxy.Envelope) error { return nil }))
	delivery := proxyruntime.NewDelivery()
	server := httptest.NewServer(NewServer(Config{
		Gateway:  gateway,
		Delivery: delivery,
		LoginHandler: proxy.LoginHandlerFunc(func(context.Context, proxy.LoginRequest) (proxy.LoginResult, error) {
			return proxy.LoginResult{
				Identity: proxy.Identity{UID: 1001, Account: "account-1"},
				Response: &rpc.Message{
					Name:    "MseLogin",
					Payload: []byte{1},
					Header:  map[string]string{rpc.HeaderPBType: "StringValue"},
				},
			}, nil
		}),
	}))
	defer server.Close()

	ws := loginConn(t, server)
	defer ws.Close(coderws.StatusNormalClosure, "done")
	ids := gateway.ConnectionIDs(1001)
	if len(ids) != 1 {
		t.Fatalf("ConnectionIDs = %v, want 1", ids)
	}

	messages := []rpc.Message{
		{Name: "RPCError", Payload: []byte{7}, Header: map[string]string{rpc.HeaderPBType: "StringValue"}},
		{Name: "StringValue", Payload: []byte{8}, Header: map[string]string{rpc.HeaderPBType: "StringValue"}},
		{Name: "MseAuth"},
	}
	for _, msg := range messages {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := delivery.Send(ctx, ids[0], msg)
		cancel()
		if err != nil {
			t.Fatalf("Delivery.Send(%q) error = %v", msg.Name, err)
		}
		head, _ := readPacket(t, ws)
		if head.Topic != msg.Name {
			t.Fatalf("downlink topic = %q, want %q", head.Topic, msg.Name)
		}
	}
}

func TestServerLoginFailureResponse(t *testing.T) {
	gateway := proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(context.Context, proxy.Envelope) error { return nil }))
	server := httptest.NewServer(NewServer(Config{
		Gateway:  gateway,
		Delivery: proxyruntime.NewDelivery(),
		LoginHandler: proxy.LoginHandlerFunc(func(context.Context, proxy.LoginRequest) (proxy.LoginResult, error) {
			return proxy.LoginResult{
				Response: &rpc.Message{
					Name:    "MseLoginFail",
					Payload: []byte{2},
					Header:  map[string]string{rpc.HeaderPBType: "StringValue"},
				},
				CloseAfterResponse: true,
			}, nil
		}),
	}))
	defer server.Close()

	ws := dial(t, server)
	defer ws.Close(coderws.StatusNormalClosure, "done")
	writePacket(t, ws, "MceLogin", "Int64Value", 0, []byte{1})
	head, body := readPacket(t, ws)
	if head.Topic != "MseLoginFail" || head.PbType != "StringValue" || !bytes.Equal(body, []byte{2}) {
		t.Fatalf("login fail response = head:%+v body:%v", head, body)
	}
	expectReadError(t, ws)
	if ids := gateway.ConnectionIDs(1001); len(ids) != 0 {
		t.Fatalf("ConnectionIDs = %v, want empty", ids)
	}
}

func TestDuplicateAllowKeepsMultipleConnections(t *testing.T) {
	gateway, server := duplicateServer(t, DuplicateAllow, nil)
	defer server.Close()
	ws1 := loginConn(t, server)
	defer ws1.Close(coderws.StatusNormalClosure, "done")
	ws2 := loginConn(t, server)
	defer ws2.Close(coderws.StatusNormalClosure, "done")
	if ids := gateway.ConnectionIDs(1001); len(ids) != 2 {
		t.Fatalf("ConnectionIDs = %v, want 2 connections", ids)
	}
}

func TestSharedGatewayAcrossServersUsesUniqueConnectionIDs(t *testing.T) {
	gateway := proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(context.Context, proxy.Envelope) error { return nil }))
	delivery := proxyruntime.NewDelivery()
	config := Config{
		Gateway:         gateway,
		Delivery:        delivery,
		DuplicatePolicy: DuplicateAllow,
		LoginHandler: proxy.LoginHandlerFunc(func(context.Context, proxy.LoginRequest) (proxy.LoginResult, error) {
			return proxy.LoginResult{
				Identity: proxy.Identity{UID: 1001},
				Response: &rpc.Message{
					Name: "MseLogin", Payload: []byte{1}, Header: map[string]string{rpc.HeaderPBType: "StringValue"},
				},
			}, nil
		}),
	}
	firstServer := httptest.NewServer(NewServer(config))
	defer firstServer.Close()
	secondServer := httptest.NewServer(NewServer(config))
	defer secondServer.Close()
	first := loginConn(t, firstServer)
	defer first.Close(coderws.StatusNormalClosure, "done")
	second := loginConn(t, secondServer)
	defer second.Close(coderws.StatusNormalClosure, "done")
	ids := gateway.ConnectionIDs(1001)
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("ConnectionIDs = %v, want two unique connections", ids)
	}
}

func TestDuplicateKickOldClosesExistingConnection(t *testing.T) {
	downlink := &recordingDownlink{}
	gateway, server := duplicateServer(t, DuplicateKickOld, downlink)
	defer server.Close()
	ws1 := loginConn(t, server)
	defer ws1.Close(coderws.StatusNormalClosure, "done")
	ws2 := loginConn(t, server)
	defer ws2.Close(coderws.StatusNormalClosure, "done")
	expectReadError(t, ws1)
	if ids := gateway.ConnectionIDs(1001); len(ids) != 1 {
		t.Fatalf("ConnectionIDs = %v, want 1 connection", ids)
	}
	if got := downlink.unsubscriptions(); len(got) != 1 || got[0] != 1001 {
		t.Fatalf("unsubscriptions = %v, want [1001]", got)
	}
}

func TestDuplicateRejectNewKeepsExistingConnection(t *testing.T) {
	gateway, server := duplicateServer(t, DuplicateRejectNew, nil)
	defer server.Close()
	ws1 := loginConn(t, server)
	defer ws1.Close(coderws.StatusNormalClosure, "done")
	ws2 := dial(t, server)
	defer ws2.Close(coderws.StatusNormalClosure, "done")
	writePacket(t, ws2, "MceLogin", "Int64Value", 0, []byte{1})
	expectReadError(t, ws2)
	if ids := gateway.ConnectionIDs(1001); len(ids) != 1 {
		t.Fatalf("ConnectionIDs = %v, want existing connection only", ids)
	}
}

func TestDuplicateHandlerCanOverridePolicy(t *testing.T) {
	gateway, server := duplicateServer(t, DuplicateRejectNew, nil)
	server.Config.Handler.(*Server).cfg.DuplicateHandler = DuplicateHandlerFunc(func(context.Context, DuplicateContext) (DuplicateDecision, error) {
		return DuplicateDecision{Policy: DuplicateAllow}, nil
	})
	defer server.Close()
	ws1 := loginConn(t, server)
	defer ws1.Close(coderws.StatusNormalClosure, "done")
	ws2 := loginConn(t, server)
	defer ws2.Close(coderws.StatusNormalClosure, "done")
	if ids := gateway.ConnectionIDs(1001); len(ids) != 2 {
		t.Fatalf("ConnectionIDs = %v, want duplicate handler to allow", ids)
	}
}

func TestCleanupUnsubscribesLastConnectionAndCallsLifecycle(t *testing.T) {
	downlink := &recordingDownlink{}
	disconnected := make(chan proxyruntime.Session, 1)
	server := httptest.NewServer(NewServer(Config{
		Gateway:  proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(context.Context, proxy.Envelope) error { return nil })),
		Delivery: proxyruntime.NewDelivery(),
		Downlink: downlink,
		Lifecycle: SessionLifecycleFunc(func(_ context.Context, session proxyruntime.Session) error {
			disconnected <- session
			return nil
		}),
		LoginHandler: proxy.LoginHandlerFunc(func(context.Context, proxy.LoginRequest) (proxy.LoginResult, error) {
			return proxy.LoginResult{
				Identity: proxy.Identity{UID: 1001, Account: "account-1"},
				Response: &rpc.Message{
					Name:    "MseLogin",
					Payload: []byte{1},
					Header:  map[string]string{rpc.HeaderPBType: "StringValue"},
				},
			}, nil
		}),
	}))
	defer server.Close()

	ws := loginConn(t, server)
	if err := ws.Close(coderws.StatusNormalClosure, "done"); err != nil {
		t.Fatalf("client close error = %v", err)
	}
	select {
	case session := <-disconnected:
		if session.Identity.UID != 1001 {
			t.Fatalf("disconnect uid = %d, want 1001", session.Identity.UID)
		}
	case <-time.After(time.Second):
		t.Fatal("disconnect lifecycle timeout")
	}
	eventually(t, func() bool {
		got := downlink.unsubscriptions()
		return len(got) == 1 && got[0] == 1001
	})
}

func TestServerRejectsTextFrame(t *testing.T) {
	server := httptest.NewServer(NewServer(Config{
		Gateway:      proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(context.Context, proxy.Envelope) error { return nil })),
		Delivery:     proxyruntime.NewDelivery(),
		LoginHandler: proxy.LoginHandlerFunc(func(context.Context, proxy.LoginRequest) (proxy.LoginResult, error) { return proxy.LoginResult{}, nil }),
	}))
	defer server.Close()

	ws := dial(t, server)
	defer ws.Close(coderws.StatusNormalClosure, "done")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := ws.Write(ctx, coderws.MessageText, []byte("bad")); err != nil {
		t.Fatalf("write text error = %v", err)
	}
	expectReadError(t, ws)
}

func duplicateServer(t *testing.T, policy DuplicatePolicy, downlink *recordingDownlink) (*proxyruntime.Gateway, *httptest.Server) {
	t.Helper()
	gateway := proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(context.Context, proxy.Envelope) error { return nil }))
	if downlink == nil {
		downlink = &recordingDownlink{}
	}
	handler := NewServer(Config{
		Gateway:         gateway,
		Delivery:        proxyruntime.NewDelivery(),
		Downlink:        downlink,
		DuplicatePolicy: policy,
		LoginHandler: proxy.LoginHandlerFunc(func(context.Context, proxy.LoginRequest) (proxy.LoginResult, error) {
			return proxy.LoginResult{
				Identity: proxy.Identity{UID: 1001, Account: "account-1"},
				Response: &rpc.Message{
					Name:    "MseLogin",
					Payload: []byte{1},
					Header:  map[string]string{rpc.HeaderPBType: "StringValue"},
				},
			}, nil
		}),
	})
	return gateway, httptest.NewServer(handler)
}

func loginConn(t *testing.T, server *httptest.Server) *coderws.Conn {
	t.Helper()
	ws := dial(t, server)
	writePacket(t, ws, "MceLogin", "Int64Value", 0, []byte{1})
	head, _ := readPacket(t, ws)
	if head.Topic != "MseLogin" {
		t.Fatalf("login response topic = %q, want MseLogin", head.Topic)
	}
	return ws
}

func dial(t *testing.T, server *httptest.Server) *coderws.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := coderws.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("Dial error = %v", err)
	}
	return ws
}

func writePacket(t *testing.T, ws *coderws.Conn, topic string, pbType string, inbox int32, body []byte) {
	t.Helper()
	parts, err := proxycodec.EncodePacket(&proxypb.MsgHead{Topic: topic, PbType: pbType, Inbox: inbox}, body)
	if err != nil {
		t.Fatalf("EncodePacket error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := ws.Write(ctx, coderws.MessageBinary, bytes.Join(parts, nil)); err != nil {
		t.Fatalf("Write error = %v", err)
	}
}

func readPacket(t *testing.T, ws *coderws.Conn) (*proxypb.MsgHead, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	msgType, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatalf("Read error = %v", err)
	}
	if msgType != coderws.MessageBinary {
		t.Fatalf("message type = %v, want binary", msgType)
	}
	head, body, err := proxycodec.DecodePacket(data)
	if err != nil {
		t.Fatalf("DecodePacket error = %v", err)
	}
	return head, body
}

func expectReadError(t *testing.T, ws *coderws.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, err := ws.Read(ctx); err == nil {
		t.Fatal("Read error = nil, want close/error")
	}
}

func eventually(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		if ok() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("condition timeout")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type recordingDownlink struct {
	mu     sync.Mutex
	subs   []int64
	unsubs []int64
}

type topicLoginHandler struct {
	topic       string
	validateErr error
	login       func(context.Context, proxy.LoginRequest) (proxy.LoginResult, error)
}

func (h *topicLoginHandler) Topic() string {
	return h.topic
}

func (h *topicLoginHandler) Validate() error {
	return h.validateErr
}

func (h *topicLoginHandler) Login(ctx context.Context, req proxy.LoginRequest) (proxy.LoginResult, error) {
	if h.login != nil {
		return h.login(ctx, req)
	}
	return proxy.LoginResult{}, nil
}

func (d *recordingDownlink) SubscribeUID(uid int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.subs = append(d.subs, uid)
	return nil
}

func (d *recordingDownlink) UnsubscribeUID(uid int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.unsubs = append(d.unsubs, uid)
	return nil
}

func (d *recordingDownlink) subscriptions() []int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]int64, len(d.subs))
	copy(out, d.subs)
	return out
}

func (d *recordingDownlink) unsubscriptions() []int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]int64, len(d.unsubs))
	copy(out, d.unsubs)
	return out
}
