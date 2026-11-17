package app

import (
	"context"
	"strings"
	"testing"

	account "github.com/li9chuan/aifirstdev/aifd-account"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/generated/pbmsg"
)

func TestAuthoritativeRevocationRejectsNextBusinessRequest(t *testing.T) {
	a, server, ws := startLoginTest(t)
	defer a.Close(context.Background())
	defer server.Close()
	defer ws.CloseNow()
	login := authRequest(t, a, 1001)
	writeAuth(t, ws, login)
	readPacket(t, ws)
	if err := a.Account.Service.RevokeSession(context.Background(), account.SessionRef{AccountID: login.Payload.AccountId, SessionID: login.Payload.SessionId}); err != nil {
		t.Fatal(err)
	}
	writeCounter(t, ws, 1, &pbmsg.MceCounterAdd{Delta: 7})
	expectReadError(t, ws)
	owner, ok := a.Game.Runtime.GetUser(login.Payload.Uid)
	if !ok {
		t.Fatal("user missing")
	}
	if owner.Snapshot().GetCounter().GetValue() != 0 {
		t.Fatal("revoked request mutated business state")
	}
}

func TestDemoClientObtainsSessionFromServerAuthority(t *testing.T) {
	a, server, ws := startLoginTest(t)
	defer a.Close(context.Background())
	defer server.Close()
	defer ws.CloseNow()
	login, err := RequestDemoAuth(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http")+"/ws", 1001)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Account.Service.ValidateSession(context.Background(), account.SessionRef{AccountID: login.Payload.AccountId, SessionID: login.Payload.SessionId}); err != nil {
		t.Fatal("client received unowned session", err)
	}
	writeAuth(t, ws, login)
	head, _ := readPacket(t, ws)
	if head.Topic != "MseAuth" {
		t.Fatal(head)
	}
}
