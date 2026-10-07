package client

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/auth"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/generated/pbmsg"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/internal/app"
)

func TestClientLoginAndCounter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	appInstance, server := startClientTest(t)
	defer appInstance.Close(context.Background())
	defer server.Close()

	client := newTestClient(t, ctx, server)
	defer client.Close()
	authReq, err := appInstance.Account.DemoAuth(ctx, 1001)
	if err != nil {
		t.Fatalf("DemoAuth error = %v", err)
	}
	authRsp, err := client.Login(ctx, authReq)
	if err != nil {
		t.Fatalf("Login error = %v", err)
	}
	if authRsp.GetRet() != auth.RetOK {
		t.Fatalf("auth ret = %d, want RetOK", authRsp.GetRet())
	}
	counterRsp, err := client.AddCounter(ctx, 3)
	if err != nil {
		t.Fatalf("AddCounter error = %v", err)
	}
	if counterRsp.GetUid() != 1001 || counterRsp.GetValue() != 3 || counterRsp.GetMessage() != "ok" {
		t.Fatalf("counter rsp = %+v, want uid 1001 value 3 ok", &counterRsp)
	}
}

func TestClientLoginInvalidAuth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	appInstance, server := startClientTest(t)
	defer appInstance.Close(context.Background())
	defer server.Close()

	client := newTestClient(t, ctx, server)
	defer client.Close()
	rsp, err := client.Login(ctx, &pbmsg.MceAuth{Token: "bad"})
	if err == nil {
		t.Fatal("Login error = nil, want invalid auth")
	}
	if rsp.GetRet() != auth.RetInvalidAuth {
		t.Fatalf("auth ret = %d, want RetInvalidAuth", rsp.GetRet())
	}
}

func startClientTest(t *testing.T) (*app.AllInOneApp, *httptest.Server) {
	t.Helper()
	appInstance, err := app.NewAllInOneApp(context.Background())
	if err != nil {
		t.Fatalf("NewAllInOneApp error = %v", err)
	}
	server := httptest.NewServer(appInstance.Proxy.Handler)
	return appInstance, server
}

func newTestClient(t *testing.T, ctx context.Context, server *httptest.Server) *Client {
	t.Helper()
	client, err := New(ctx, Options{
		URL:          "ws" + strings.TrimPrefix(server.URL, "http"),
		Timeout:      time.Second,
		AwaitTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New client error = %v", err)
	}
	return client
}
