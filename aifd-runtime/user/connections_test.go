package user

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	proxyruntime "github.com/li9chuan/aifirstdev/aifd-proxy/runtime"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	contracts "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	proxypb "github.com/li9chuan/aifirstdev/aifd-schema/generated/proxy"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestUserRoutesLoadOfflineUsersButRequireLiveClientBinding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	setupMemoryDB(t)
	store := newInt64Store(t)
	if _, err := store.Create(ctx, 42); err != nil {
		t.Fatal(err)
	}
	if err := rdb.FlushDefaultDB(ctx); err != nil {
		t.Fatal(err)
	}
	bus := rpc.NewLocalBus(rpc.LocalConfig{})
	defer bus.Close(context.Background())
	rt := NewRuntime(rpc.NewRegistry(), store, nil, WithBus(bus), WithDefaultHeartbeat())
	defer rt.Close(context.Background())
	if err := rt.RegisterRPC(); err != nil {
		t.Fatal(err)
	}
	handler := func(s *rpc.Session, u *User[*wrapperspb.Int64Value], _ *wrapperspb.StringValue) {
		s.Response(wrapperspb.Int64(u.UID()))
	}
	if err := rt.Server("UserRead", handler); err != nil {
		t.Fatal(err)
	}
	if err := rt.Client("MceRead", handler); err != nil {
		t.Fatal(err)
	}
	if err := rpc.Start(ctx, bus, rt.Registry()); err != nil {
		t.Fatal(err)
	}
	read, _ := contracts.NewMessagePB("UserRead", wrapperspb.String("offline"))
	if _, err := checkedRequest(ctx, bus, 42, read, contracts.RequestOptions{Timeout: time.Second}); err != nil {
		t.Fatal(err)
	}
	owner, ok := rt.GetUser(42)
	if !ok || rt.OnlineConnections(42) != 0 {
		t.Fatal("offline route created online state")
	}
	if _, err := checkedRequest(ctx, bus, 43, read, contracts.RequestOptions{Timeout: time.Second}); err == nil {
		t.Fatal("route implicitly created missing user")
	}

	session := proxyruntime.Session{ProxyInstanceID: "boot", ConnectionID: "A", Identity: proxy.Identity{UID: 42}}
	lifecycle := proxyruntime.NewRPCSessionLifecycle(bus)
	if err := lifecycle.OnConnect(ctx, session); err != nil {
		t.Fatal(err)
	}
	read.Name = "MceRead"
	read.Header[proxy.HeaderCallerKind] = proxy.CallerClient
	read.Header[proxy.HeaderProxyInstanceID] = session.ProxyInstanceID
	read.Header[proxy.HeaderConnectionID] = session.ConnectionID
	if _, err := checkedRequest(ctx, bus, 42, read, contracts.RequestOptions{Timeout: time.Second}); err != nil {
		t.Fatal(err)
	}
	loaded, _ := rt.GetUser(42)
	if loaded != owner || rt.OnlineConnections(42) != 1 {
		t.Fatal("client route duplicated user or connection")
	}
	if err := lifecycle.OnDisconnect(ctx, session, proxypb.SessionEndReason_SESSION_END_REASON_DISCONNECTED); err != nil {
		t.Fatal(err)
	}
	// Both operations use the same UID runner, so this request follows the close.
	if _, err := checkedRequest(ctx, bus, 42, read, contracts.RequestOptions{Timeout: time.Second}); err == nil {
		t.Fatal("closed client route admitted")
	}
	if loaded, ok := rt.GetUser(42); !ok || loaded != owner {
		t.Fatal("closing a connection destroyed user")
	}

	// Reconstructing the business owner does not infer connection state from its DB.
	restarted := NewRuntime(rpc.NewRegistry(), store, nil)
	defer restarted.Close(context.Background())
	if _, err := restarted.LoadUser(ctx, 42); err != nil {
		t.Fatal(err)
	}
	internal := restarted.NewSession(ctx, 42)
	if err := restarted.RenewConnection(internal); !errors.Is(err, proxy.ErrClientSessionMissing) {
		t.Fatalf("internal session renewed connection: %v", err)
	}
	if restarted.OnlineConnections(42) != 0 {
		t.Fatal("restart restored fictional online state")
	}
}

func TestLifecycleRejectsUntrustedOrDelayedOpenWithoutLoadingUser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	setupMemoryDB(t)
	bus := rpc.NewLocalBus(rpc.LocalConfig{})
	defer bus.Close(context.Background())
	rt := NewRuntime(rpc.NewRegistry(), newInt64Store(t), nil)
	defer rt.Close(context.Background())
	if err := rt.RegisterRPC(); err != nil {
		t.Fatal(err)
	}
	if err := rpc.Start(ctx, bus, rt.Registry()); err != nil {
		t.Fatal(err)
	}
	ref := &proxypb.ClientSessionRef{ProxyInstanceId: "boot", ConnectionId: "A"}
	msg, _ := contracts.NewMessagePB("", &proxypb.OpenClientSessionRequest{Session: ref})
	if _, err := checkedRequest(ctx, bus, 42, msg, contracts.RequestOptions{Timeout: time.Second}); err == nil {
		t.Fatal("forged open admitted")
	}
	msg.Header[proxy.HeaderCallerKind] = proxy.CallerProxy
	msg.Header[proxy.HeaderProxyInstanceID] = "boot"
	msg.Header[proxy.HeaderConnectionID] = "A"
	msg.Header[proxy.HeaderSessionDeadline] = strconv.FormatInt(time.Now().Add(-time.Second).UnixNano(), 10)
	if _, err := checkedRequest(ctx, bus, 42, msg, contracts.RequestOptions{Timeout: time.Second}); err == nil {
		t.Fatal("delayed open admitted")
	}
	lifecycle := proxyruntime.NewRPCSessionLifecycle(bus)
	if err := lifecycle.OnConnect(ctx, proxyruntime.Session{ProxyInstanceID: "boot", ConnectionID: "A", Identity: proxy.Identity{UID: 42}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := rt.GetUser(42); ok {
		t.Fatal("lease opening created user")
	}
	if rt.OnlineConnections(42) != 1 {
		t.Fatal("valid lease missing")
	}
}

func checkedRequest(ctx context.Context, bus contracts.Bus, uid int64, msg contracts.Message, opts contracts.RequestOptions) (contracts.Message, error) {
	rsp, err := bus.Request(ctx, uid, msg, opts)
	if err == nil {
		err = contracts.StatusFromMessage(rsp)
	}
	return rsp, err
}
