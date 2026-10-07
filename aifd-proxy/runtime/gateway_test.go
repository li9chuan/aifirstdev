package runtime

import (
	"context"
	"testing"

	"github.com/li9chuan/aifirstdev/aifd-proxy/adapters/local"
	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

func TestGatewayAcceptReceiveAndDeliver(t *testing.T) {
	ctx := context.Background()
	upstream := local.NewUpstreamRecorder()
	gateway := NewGateway(proxy.AuthenticatorFunc(func(context.Context, proxy.AuthRequest) (proxy.Identity, error) {
		return proxy.Identity{UID: 1001, Account: "account-1", ServerID: 1}, nil
	}), upstream)
	delivery := NewDelivery()
	conn := local.NewConnection("conn-1")
	if err := delivery.Add(conn); err != nil {
		t.Fatalf("delivery Add error = %v", err)
	}

	session, err := gateway.Accept(ctx, conn, proxy.AuthRequest{Token: "ok"})
	if err != nil {
		t.Fatalf("Accept error = %v", err)
	}
	if session.Identity.UID != 1001 {
		t.Fatalf("session uid = %d, want 1001", session.Identity.UID)
	}
	if err := gateway.Receive(ctx, "conn-1", rpc.Message{Name: "McePing", Payload: []byte("ping")}); err != nil {
		t.Fatalf("Receive error = %v", err)
	}
	envs := upstream.Envelopes()
	if len(envs) != 1 {
		t.Fatalf("envelopes len = %d, want 1", len(envs))
	}
	if envs[0].UID != 1001 || envs[0].Message.Name != "McePing" {
		t.Fatalf("unexpected envelope = %+v", envs[0])
	}

	ids := gateway.ConnectionIDs(1001)
	if len(ids) != 1 || ids[0] != "conn-1" {
		t.Fatalf("ConnectionIDs = %v, want [conn-1]", ids)
	}
	if err := delivery.Broadcast(ctx, ids, rpc.Message{Name: "MsePong"}); err != nil {
		t.Fatalf("Broadcast error = %v", err)
	}
	sent := conn.Sent()
	if len(sent) != 1 || sent[0].Name != "MsePong" {
		t.Fatalf("sent = %+v, want MsePong", sent)
	}

	if !gateway.Disconnect("conn-1") {
		t.Fatal("Disconnect = false, want true")
	}
	if ids := gateway.ConnectionIDs(1001); len(ids) != 0 {
		t.Fatalf("ConnectionIDs after disconnect = %v, want empty", ids)
	}
}

func TestGatewayAcceptIdentitySessionsByUIDAndDisconnectSession(t *testing.T) {
	gateway := NewGateway(nil, proxy.UpstreamFunc(func(context.Context, proxy.Envelope) error { return nil }))
	conn1 := local.NewConnection("conn-1")
	conn2 := local.NewConnection("conn-2")
	if _, err := gateway.AcceptIdentity(conn1, proxy.Identity{UID: 1001, Account: "a"}); err != nil {
		t.Fatalf("AcceptIdentity conn1 error = %v", err)
	}
	if _, err := gateway.AcceptIdentity(conn2, proxy.Identity{UID: 1001, Account: "a"}); err != nil {
		t.Fatalf("AcceptIdentity conn2 error = %v", err)
	}
	sessions := gateway.SessionsByUID(1001)
	if len(sessions) != 2 || sessions[0].ConnectionID != "conn-1" || sessions[1].ConnectionID != "conn-2" {
		t.Fatalf("SessionsByUID = %+v", sessions)
	}
	removed, ok := gateway.DisconnectSession("conn-1")
	if !ok || removed.ConnectionID != "conn-1" || removed.Identity.UID != 1001 {
		t.Fatalf("DisconnectSession = %+v %v", removed, ok)
	}
	ids := gateway.ConnectionIDs(1001)
	if len(ids) != 1 || ids[0] != "conn-2" {
		t.Fatalf("ConnectionIDs = %v, want [conn-2]", ids)
	}
}

func TestDeliveryCloseClosesAndRemovesConnection(t *testing.T) {
	ctx := context.Background()
	delivery := NewDelivery()
	conn := local.NewConnection("conn-1")
	if err := delivery.Add(conn); err != nil {
		t.Fatalf("Add error = %v", err)
	}
	if err := delivery.Close(ctx, "conn-1"); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if !conn.Closed() {
		t.Fatal("connection not closed")
	}
	if err := delivery.Send(ctx, "conn-1", rpc.Message{Name: "MsePong"}); err != ErrConnectionNotFound {
		t.Fatalf("Send after close error = %v, want ErrConnectionNotFound", err)
	}
}
