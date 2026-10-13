package app

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/auth"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/counter"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/generated/pbmsg"
	proxycodec "github.com/li9chuan/aifirstdev/aifd-proxy/codec"
	proxypb "github.com/li9chuan/aifirstdev/aifd-schema/generated/proxy"
	"google.golang.org/protobuf/proto"
)

func TestWebSocketLoginSuccess(t *testing.T) {
	app, server, ws := startLoginTest(t)
	defer app.Close(context.Background())
	defer server.Close()
	defer ws.Close(coderws.StatusNormalClosure, "done")

	writeAuth(t, ws, authRequest(t, app, 1001))
	head, body := readPacket(t, ws)
	if head.Topic != auth.TopicMseAuth || head.PbType != auth.TopicMseAuth {
		t.Fatalf("head = %+v, want MseAuth topic and pbType", head)
	}
	var rsp pbmsg.MseAuth
	if err := proto.Unmarshal(body, &rsp); err != nil {
		t.Fatalf("MseAuth unmarshal error = %v", err)
	}
	if rsp.GetRet() != auth.RetOK {
		t.Fatalf("rsp = %+v, want successful login", &rsp)
	}
	owner, ok := app.Game.Runtime.GetUser(1001)
	if !ok {
		t.Fatal("owner 1001 is not active")
	}
	firstAuth := owner.Snapshot().GetAuth()
	if firstAuth.GetUid() != 1001 || firstAuth.GetAccountId() != 1001 || !firstAuth.GetIsNewAccount() {
		t.Fatalf("auth payload = %+v, want first login identity", firstAuth)
	}
	if err := app.DB.Flush(context.Background()); err != nil {
		t.Fatalf("DB Flush error = %v", err)
	}

	secondWS := dial(t, server)
	defer secondWS.Close(coderws.StatusNormalClosure, "done")
	writeAuth(t, secondWS, authRequest(t, app, 1001))
	_, secondBody := readPacket(t, secondWS)
	var secondRsp pbmsg.MseAuth
	if err := proto.Unmarshal(secondBody, &secondRsp); err != nil {
		t.Fatalf("second MseAuth unmarshal error = %v", err)
	}
	if secondRsp.GetRet() != auth.RetOK {
		t.Fatalf("second rsp = %+v, want existing login", &secondRsp)
	}
	owner, ok = app.Game.Runtime.GetUser(1001)
	if !ok {
		t.Fatal("owner 1001 is not active after second login")
	}
	secondAuth := owner.Snapshot().GetAuth()
	if secondAuth.GetAccountId() != firstAuth.GetAccountId() || secondAuth.GetIsNewAccount() {
		t.Fatalf("second auth payload = %+v, want existing account", secondAuth)
	}
}

func TestWebSocketCounterAfterLogin(t *testing.T) {
	app, server, ws := startLoginTest(t)
	defer app.Close(context.Background())
	defer server.Close()
	defer ws.Close(coderws.StatusNormalClosure, "done")

	writeAuth(t, ws, authRequest(t, app, 1001))
	_, _ = readPacket(t, ws)

	writeCounter(t, ws, 1, &pbmsg.MceCounterAdd{Delta: 3})
	head, body := readPacket(t, ws)
	if head.Topic != counter.TopicMseCounterState || head.PbType != counter.TopicMseCounterState || head.Inbox != 1 {
		t.Fatalf("head = %+v, want MseCounterState topic/pbType and inbox 1", head)
	}
	var rsp pbmsg.MseCounterState
	if err := proto.Unmarshal(body, &rsp); err != nil {
		t.Fatalf("MseCounterState unmarshal error = %v", err)
	}
	if rsp.GetUid() != 1001 || rsp.GetValue() != 3 || rsp.GetLimit() != 10 || rsp.GetMessage() != "ok" {
		t.Fatalf("rsp = %+v, want counter value 3", &rsp)
	}
	if err := app.DB.Flush(context.Background()); err != nil {
		t.Fatalf("DB Flush error = %v", err)
	}
	owner, ok := app.Game.Runtime.GetUser(1001)
	if !ok {
		t.Fatal("owner 1001 is not active")
	}
	if value := owner.Snapshot().GetCounter().GetValue(); value != 3 {
		t.Fatalf("counter value = %d, want 3", value)
	}
	global := waitForWorldTotal(t, app, 3)
	if global.GetTotalAdd() != 3 || global.GetLastUid() != 1001 {
		t.Fatalf("global state = %+v, want total 3 last uid 1001", global)
	}
	if !hasObserveEvent(app, "counter.add") {
		t.Fatal("observe event counter.add missing")
	}
}

func TestCounterConfigReloadChangesLimit(t *testing.T) {
	app, server, ws := startLoginTest(t)
	defer app.Close(context.Background())
	defer server.Close()
	defer ws.Close(coderws.StatusNormalClosure, "done")

	if err := app.Config.SetCounterLimit(context.Background(), 2); err != nil {
		t.Fatalf("SetCounterLimit error = %v", err)
	}
	writeAuth(t, ws, authRequest(t, app, 1001))
	_, _ = readPacket(t, ws)
	writeCounter(t, ws, 1, &pbmsg.MceCounterAdd{Delta: 5})
	_, body := readPacket(t, ws)
	var rsp pbmsg.MseCounterState
	if err := proto.Unmarshal(body, &rsp); err != nil {
		t.Fatalf("MseCounterState unmarshal error = %v", err)
	}
	if rsp.GetValue() != 2 || rsp.GetLimit() != 2 || rsp.GetMessage() != "limit" {
		t.Fatalf("rsp = %+v, want capped counter", &rsp)
	}
}

func TestWebSocketLoginInvalidTokenClosesAfterResponse(t *testing.T) {
	app, server, ws := startLoginTest(t)
	defer app.Close(context.Background())
	defer server.Close()
	defer ws.Close(coderws.StatusNormalClosure, "done")

	writeAuth(t, ws, &pbmsg.MceAuth{Token: "bad"})
	head, body := readPacket(t, ws)
	if head.Topic != auth.TopicMseAuth || head.PbType != auth.TopicMseAuth {
		t.Fatalf("head = %+v, want MseAuth topic and pbType", head)
	}
	var rsp pbmsg.MseAuth
	if err := proto.Unmarshal(body, &rsp); err != nil {
		t.Fatalf("MseAuth unmarshal error = %v", err)
	}
	if rsp.GetRet() != auth.RetInvalidAuth {
		t.Fatalf("ret = %d, want invalid auth", rsp.GetRet())
	}
	expectReadError(t, ws)
}

func startLoginTest(t *testing.T) (*AllInOneApp, *httptest.Server, *coderws.Conn) {
	t.Helper()
	app, err := NewAllInOneApp(context.Background())
	if err != nil {
		t.Fatalf("NewAllInOneApp error = %v", err)
	}
	server := httptest.NewServer(app.Proxy.Handler)
	ws := dial(t, server)
	return app, server, ws
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

func writeAuth(t *testing.T, ws *coderws.Conn, req *pbmsg.MceAuth) {
	t.Helper()
	writePB(t, ws, auth.TopicMceAuth, 0, req)
}

func authRequest(t *testing.T, app *AllInOneApp, uid int64) *pbmsg.MceAuth {
	t.Helper()
	req, err := app.Account.DemoAuth(context.Background(), uid)
	if err != nil {
		t.Fatalf("DemoAuth error = %v", err)
	}
	return req
}

func writeCounter(t *testing.T, ws *coderws.Conn, inbox int32, req *pbmsg.MceCounterAdd) {
	t.Helper()
	writePB(t, ws, counter.TopicMceCounterAdd, inbox, req)
}

func writePB(t *testing.T, ws *coderws.Conn, topic string, inbox int32, pb proto.Message) {
	t.Helper()
	body, err := proto.Marshal(pb)
	if err != nil {
		t.Fatalf("%s marshal error = %v", topic, err)
	}
	parts, err := proxycodec.EncodePacket(&proxypb.MsgHead{
		Topic:  topic,
		PbType: topic,
		Inbox:  inbox,
	}, body)
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

func hasObserveEvent(app *AllInOneApp, name string) bool {
	for _, event := range app.Observe.Events() {
		if event.Name == name {
			return true
		}
	}
	return false
}

func waitForWorldTotal(t *testing.T, app *AllInOneApp, want int64) *pbmsg.CounterWorldState {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		global := app.World.Counter.Snapshot()
		if global.GetTotalAdd() >= want {
			return global
		}
		if time.Now().After(deadline) {
			return global
		}
		time.Sleep(5 * time.Millisecond)
	}
}
