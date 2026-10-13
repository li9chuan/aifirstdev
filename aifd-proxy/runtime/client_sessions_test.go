package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	proxypb "github.com/li9chuan/aifirstdev/aifd-schema/generated/proxy"
)

func TestClientLeasesKeepDevicesAndProxyGenerationsIndependent(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	var ended []*proxypb.ClientSessionRef
	m := NewClientSessions(ClientSessionOptions{TTL: 10 * time.Second, Now: func() time.Time { return now },
		OnClosed: func(_ context.Context, _ int64, ref *proxypb.ClientSessionRef, reason proxypb.SessionEndReason) error {
			if reason != proxypb.SessionEndReason_SESSION_END_REASON_EXPIRED {
				t.Fatalf("end reason=%v", reason)
			}
			ended = append(ended, ref)
			return nil
		},
	})
	a := &proxypb.ClientSessionRef{ProxyInstanceId: "first-boot", ConnectionId: "1"}
	b := &proxypb.ClientSessionRef{ProxyInstanceId: "second-boot", ConnectionId: "1"}
	for _, ref := range []*proxypb.ClientSessionRef{a, b, a} {
		if err := m.Open(ctx, 42, ref); err != nil {
			t.Fatal(err)
		}
	}
	if m.Count(42) != 2 {
		t.Fatal("duplicate Open changed connection count")
	}
	if err := m.Renew(ctx, 43, a); !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("UID hijack: %v", err)
	}
	now = now.Add(5 * time.Second)
	if err := m.Validate(ctx, 42, b); err != nil {
		t.Fatal(err)
	}
	if err := m.Renew(ctx, 42, a); err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Second)
	if err := m.Expire(ctx); err != nil {
		t.Fatal(err)
	}
	if m.Count(42) != 1 || len(ended) != 1 || ended[0].GetProxyInstanceId() != "second-boot" {
		t.Fatalf("expiry crossed devices: %d %v", m.Count(42), ended)
	}
	if err := m.Renew(ctx, 42, b); !errors.Is(err, ErrSessionNotBound) {
		t.Fatalf("expired ref renewed: %v", err)
	}
	if err := m.Open(ctx, 42, b); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("retired ref reopened: %v", err)
	}
	if err := m.Validate(ctx, 42, a); err != nil {
		t.Fatalf("live device invalidated: %v", err)
	}
}

func TestCloseBeforeOpenAndLateHeartbeatCannotRecreateLease(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	closed := 0
	m := NewClientSessions(ClientSessionOptions{TTL: time.Second, Now: func() time.Time { return now }, OnClosed: func(context.Context, int64, *proxypb.ClientSessionRef, proxypb.SessionEndReason) error {
		closed++
		return nil
	}})
	ref := &proxypb.ClientSessionRef{ProxyInstanceId: "boot", ConnectionId: "old"}
	if err := m.End(ctx, 42, ref, proxypb.SessionEndReason_SESSION_END_REASON_DISCONNECTED); err != nil {
		t.Fatal(err)
	}
	if err := m.Open(ctx, 42, ref); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("reordered Open: %v", err)
	}
	now = now.Add(2 * time.Second)
	if err := m.Expire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Renew(ctx, 42, ref); !errors.Is(err, ErrSessionNotBound) {
		t.Fatalf("late heartbeat recreated lease: %v", err)
	}
	if m.Count(42) != 0 || closed != 0 {
		t.Fatal("unopened ref produced online state or duplicate close callback")
	}
	if err := m.Open(ctx, 42, &proxypb.ClientSessionRef{ConnectionId: "partial"}); !errors.Is(err, proxy.ErrClientSessionMissing) {
		t.Fatalf("partial identity: %v", err)
	}
}

func TestLeaseHooksRunOutsideLockAndShutdownIsIdempotent(t *testing.T) {
	ctx := context.Background()
	var m *ClientSessions
	closed := 0
	m = NewClientSessions(ClientSessionOptions{OnOpened: func(_ context.Context, uid int64, _ *proxypb.ClientSessionRef) error {
		if m.Count(uid) != 1 {
			t.Fatal("hook cannot inspect lease state")
		}
		return nil
	},
		OnClosed: func(_ context.Context, uid int64, _ *proxypb.ClientSessionRef, reason proxypb.SessionEndReason) error {
			if m.Count(uid) != 0 || reason != proxypb.SessionEndReason_SESSION_END_REASON_SHUTDOWN {
				t.Fatal("shutdown hook state")
			}
			closed++
			return nil
		}})
	ref := &proxypb.ClientSessionRef{ProxyInstanceId: "boot", ConnectionId: "1"}
	if err := m.Open(ctx, 42, ref); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if closed != 1 {
		t.Fatalf("close hooks=%d", closed)
	}
	if err := m.Open(ctx, 42, ref); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("activation after shutdown: %v", err)
	}
}
