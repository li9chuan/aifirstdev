package websocket

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	proxyruntime "github.com/li9chuan/aifirstdev/aifd-proxy/runtime"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	proxypb "github.com/li9chuan/aifirstdev/aifd-schema/generated/proxy"
)

type revalidatorFunc func(context.Context, proxy.AuthContext) error

func (f revalidatorFunc) Revalidate(ctx context.Context, a proxy.AuthContext) error { return f(ctx, a) }

func testAuthIdentity(until time.Time) proxy.Identity {
	return proxy.Identity{UID: 42, BackendID: "external", Account: "account-uuid", ValidUntil: until}
}

func TestAuthenticationModeConfiguration(t *testing.T) {
	r := revalidatorFunc(func(context.Context, proxy.AuthContext) error { return nil })
	for _, tc := range []struct {
		name              string
		mode              proxy.AuthCheckMode
		checker           proxy.AuthRevalidator
		interval, timeout time.Duration
		valid             bool
	}{
		{"unspecified", proxy.AuthCheckUnspecified, nil, 0, 0, false},
		{"expiry", proxy.AuthCheckExpiryOnly, nil, 0, 0, true},
		{"expiry-checker", proxy.AuthCheckExpiryOnly, r, 0, 0, false},
		{"expiry-duration", proxy.AuthCheckExpiryOnly, nil, time.Second, 0, false},
		{"periodic-defaults", proxy.AuthCheckPeriodic, r, 0, 0, true},
		{"per-request-defaults", proxy.AuthCheckPerRequest, r, 0, 0, true},
		{"missing-checker", proxy.AuthCheckPeriodic, nil, 0, 0, false},
		{"negative-interval", proxy.AuthCheckPeriodic, r, -time.Second, 0, false},
		{"negative-timeout", proxy.AuthCheckPerRequest, r, 0, -time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer(Config{AuthCheckMode: tc.mode, AuthRevalidator: tc.checker, AuthRecheckInterval: tc.interval, AuthCheckTimeout: tc.timeout, Gateway: proxyruntime.NewGateway(nil, nil), HandshakeHandler: proxy.HandshakeHandlerFunc(func(context.Context, proxy.AuthRequest) (proxy.HandshakeResult, error) {
				return proxy.HandshakeResult{}, nil
			})})
			if err := s.Validate(); (err == nil) != tc.valid {
				t.Fatal(err)
			}
			if tc.valid && tc.mode != proxy.AuthCheckExpiryOnly && (s.cfg.AuthRecheckInterval != 30*time.Second || s.cfg.AuthCheckTimeout != 5*time.Second) {
				t.Fatal("normalization", s.cfg)
			}
			if tc.valid && tc.mode == proxy.AuthCheckExpiryOnly && (s.cfg.AuthRecheckInterval != 0 || s.cfg.AuthCheckTimeout != 0) {
				t.Fatal("expiry mode gained queries")
			}
		})
	}
}

func TestInitialAuthenticationAndFirstProtectedRequest(t *testing.T) {
	for _, handshake := range []bool{false, true} {
		t.Run(fmt.Sprint(handshake), func(t *testing.T) {
			var initial, checks atomic.Int32
			forwarded := make(chan proxy.Envelope, 2)
			identity := testAuthIdentity(time.Now().Add(time.Hour))
			cfg := Config{AuthCheckMode: proxy.AuthCheckPerRequest, AuthRecheckInterval: time.Hour, AuthRevalidator: revalidatorFunc(func(_ context.Context, a proxy.AuthContext) error {
				checks.Add(1)
				if a.SessionID != "" || a.Account != identity.Account {
					return errors.New("snapshot changed")
				}
				return nil
			}), Gateway: proxyruntime.NewGateway(nil, proxy.UpstreamFunc(func(_ context.Context, e proxy.Envelope) error { forwarded <- e; return nil }))}
			if handshake {
				cfg.HandshakeHandler = proxy.HandshakeHandlerFunc(func(context.Context, proxy.AuthRequest) (proxy.HandshakeResult, error) {
					initial.Add(1)
					return proxy.HandshakeResult{Handled: true, Identity: identity}, nil
				})
			} else {
				cfg.LoginHandler = proxy.LoginHandlerFunc(func(context.Context, proxy.LoginRequest) (proxy.LoginResult, error) {
					initial.Add(1)
					return proxy.LoginResult{Identity: identity, Response: &rpc.Message{Name: "MseLogin"}}, nil
				})
			}
			s := NewServer(cfg)
			server := httptest.NewServer(s)
			defer server.Close()
			defer s.Close(context.Background())
			ws := dial(t, server)
			defer ws.CloseNow()
			if !handshake {
				writePacket(t, ws, "MceLogin", "", 0, nil)
				readPacket(t, ws)
			}
			eventually(t, func() bool { return len(s.cfg.Gateway.ConnectionIDs(42)) == 1 })
			if initial.Load() != 1 || checks.Load() != 0 {
				t.Fatalf("initial=%d added recheck=%d", initial.Load(), checks.Load())
			}
			for i := int32(1); i <= 2; i++ {
				writePacket(t, ws, "MceProtected", "", 0, nil)
				select {
				case e := <-forwarded:
					if checks.Load() != i || e.Auth.Account != identity.Account {
						t.Fatal("protected query skipped", checks.Load(), e)
					}
				case <-time.After(time.Second):
					t.Fatal("forward timeout")
				}
			}
		})
	}
}

func TestInvalidIdentityCannotActivateEitherLoginPath(t *testing.T) {
	for _, handshake := range []bool{false, true} {
		for _, invalid := range []string{"deadline", "backend", "account"} {
			t.Run(fmt.Sprintf("%v-%s", handshake, invalid), func(t *testing.T) {
				identity := testAuthIdentity(time.Now().Add(time.Hour))
				switch invalid {
				case "deadline":
					identity.ValidUntil = time.Now().Add(-time.Second)
				case "backend":
					identity.BackendID = ""
				case "account":
					identity.Account = ""
				}
				var activated atomic.Int32
				cfg := Config{AuthCheckMode: proxy.AuthCheckExpiryOnly, Gateway: proxyruntime.NewGateway(nil, nil), Lifecycle: SessionLifecycleFuncs{Connected: func(context.Context, proxyruntime.Session) error { activated.Add(1); return nil }}}
				if handshake {
					cfg.HandshakeHandler = proxy.HandshakeHandlerFunc(func(context.Context, proxy.AuthRequest) (proxy.HandshakeResult, error) {
						return proxy.HandshakeResult{Handled: true, Identity: identity}, nil
					})
					s := NewServer(cfg)
					rec := httptest.NewRecorder()
					s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
					if rec.Code != http.StatusUnauthorized {
						t.Fatal(rec.Code)
					}
				} else {
					cfg.LoginHandler = proxy.LoginHandlerFunc(func(context.Context, proxy.LoginRequest) (proxy.LoginResult, error) {
						return proxy.LoginResult{Identity: identity}, nil
					})
					s := NewServer(cfg)
					server := httptest.NewServer(s)
					defer server.Close()
					ws := dial(t, server)
					defer ws.CloseNow()
					writePacket(t, ws, "MceLogin", "", 0, nil)
					expectReadError(t, ws)
				}
				if activated.Load() != 0 {
					t.Fatal("invalid identity activated lease")
				}
			})
		}
	}
}

func TestPushOnlyConnectionExpiresAndEndsLease(t *testing.T) {
	for _, mode := range []proxy.AuthCheckMode{proxy.AuthCheckExpiryOnly, proxy.AuthCheckPeriodic, proxy.AuthCheckPerRequest} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			disconnected := make(chan struct{}, 1)
			cfg := Config{AuthCheckMode: mode, Gateway: proxyruntime.NewGateway(nil, nil), HandshakeHandler: proxy.HandshakeHandlerFunc(func(context.Context, proxy.AuthRequest) (proxy.HandshakeResult, error) {
				return proxy.HandshakeResult{Handled: true, Identity: testAuthIdentity(time.Now().Add(250 * time.Millisecond))}, nil
			}), Lifecycle: SessionLifecycleFuncs{Disconnected: func(context.Context, proxyruntime.Session, proxypb.SessionEndReason) error {
				disconnected <- struct{}{}
				return nil
			}}}
			if mode != proxy.AuthCheckExpiryOnly {
				cfg.AuthRecheckInterval = time.Hour
				cfg.AuthRevalidator = revalidatorFunc(func(context.Context, proxy.AuthContext) error { t.Error("unexpected initial recheck"); return nil })
			}
			s := NewServer(cfg)
			server := httptest.NewServer(s)
			defer server.Close()
			defer s.Close(context.Background())
			ws := dial(t, server)
			defer ws.CloseNow()
			eventually(t, func() bool { return len(s.cfg.Gateway.ConnectionIDs(42)) == 1 })
			id := s.cfg.Gateway.ConnectionIDs(42)[0]
			if err := s.cfg.Delivery.Send(context.Background(), id, rpc.Message{Name: "MsePush"}); err != nil {
				t.Fatal(err)
			}
			if head, _ := readPacket(t, ws); head.Topic != "MsePush" {
				t.Fatal(head)
			}
			expectReadError(t, ws)
			select {
			case <-disconnected:
			case <-time.After(time.Second):
				t.Fatal("expired lease remained")
			}
			if len(s.cfg.Gateway.ConnectionIDs(42)) != 0 {
				t.Fatal("expired membership retained")
			}
			if err := s.cfg.Delivery.Send(context.Background(), id, rpc.Message{Name: "MseLate"}); err == nil {
				t.Fatal("expired push accepted")
			}
		})
	}
}

func TestFailedAndTimedOutRevalidationClosesIdleConnection(t *testing.T) {
	for _, mode := range []proxy.AuthCheckMode{proxy.AuthCheckPeriodic, proxy.AuthCheckPerRequest} {
		for _, timeout := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-%v", mode, timeout), func(t *testing.T) {
				var calls atomic.Int32
				s := NewServer(Config{AuthCheckMode: mode, AuthRecheckInterval: 20 * time.Millisecond, AuthCheckTimeout: 30 * time.Millisecond, AuthRevalidator: revalidatorFunc(func(ctx context.Context, _ proxy.AuthContext) error {
					calls.Add(1)
					if timeout {
						<-ctx.Done()
						return ctx.Err()
					}
					return errors.New("revoked")
				}), Gateway: proxyruntime.NewGateway(nil, nil), HandshakeHandler: proxy.HandshakeHandlerFunc(func(context.Context, proxy.AuthRequest) (proxy.HandshakeResult, error) {
					return proxy.HandshakeResult{Handled: true, Identity: testAuthIdentity(time.Now().Add(time.Hour))}, nil
				})})
				server := httptest.NewServer(s)
				defer server.Close()
				defer s.Close(context.Background())
				ws := dial(t, server)
				defer ws.CloseNow()
				expectReadError(t, ws)
				eventually(t, func() bool { return len(s.cfg.Gateway.ConnectionIDs(42)) == 0 })
				if calls.Load() != 1 {
					t.Fatal("unexpected revalidation count", calls.Load())
				}
			})
		}
	}
}

func TestPeriodicRequestDoesNotRelyOnDelayedTimer(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	a := newConnectionAuth(Config{AuthCheckMode: proxy.AuthCheckPeriodic, AuthRecheckInterval: time.Minute, AuthCheckTimeout: time.Second, AuthRevalidator: revalidatorFunc(func(context.Context, proxy.AuthContext) error { close(entered); <-release; return nil })}, testAuthIdentity(time.Now().Add(time.Hour)).AuthContext(), func() {})
	a.nextCheck.Store(time.Now().Add(-time.Second).UnixNano())
	done := make(chan error, 1)
	go func() { done <- a.check(context.Background(), true) }()
	<-entered
	select {
	case <-done:
		t.Fatal("expired check window passed")
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestQueuedSendChecksDeadlineAfterAcquiringWriteLock(t *testing.T) {
	for _, deliveryPath := range []string{"direct", "unicast", "broadcast"} {
		t.Run(deliveryPath, func(t *testing.T) {
			terminated := make(chan struct{})
			conn := newConnection(context.Background(), "queued", nil)
			defer conn.cancel()
			a := newConnectionAuth(Config{AuthCheckMode: proxy.AuthCheckExpiryOnly}, testAuthIdentity(time.Now().Add(40*time.Millisecond)).AuthContext(), func() { close(terminated) })
			conn.auth.Store(a)
			d := proxyruntime.NewDelivery()
			if err := d.Add(conn); err != nil {
				t.Fatal(err)
			}
			conn.mu.Lock()
			done := make(chan error, 1)
			go func() {
				msg := rpc.Message{Name: "MseQueued"}
				switch deliveryPath {
				case "direct":
					done <- conn.Send(context.Background(), msg)
				case "unicast":
					done <- d.Send(context.Background(), conn.ID(), msg)
				case "broadcast":
					done <- d.Broadcast(context.Background(), []string{conn.ID()}, msg)
				}
			}()
			time.Sleep(time.Until(a.snapshot.ValidUntil) + time.Millisecond)
			conn.mu.Unlock()
			if err := <-done; !errors.Is(err, proxy.ErrAuthExpired) {
				t.Fatal(err)
			}
			select {
			case <-terminated:
			case <-time.After(time.Second):
				t.Fatal("send did not initiate cleanup")
			}
		})
	}
}

func TestTimeoutInvalidatesBeforeLateCheckerReturns(t *testing.T) {
	entered, release, terminated := make(chan struct{}), make(chan struct{}), make(chan struct{})
	a := newConnectionAuth(Config{AuthCheckMode: proxy.AuthCheckPerRequest, AuthRecheckInterval: time.Hour, AuthCheckTimeout: 20 * time.Millisecond, AuthRevalidator: revalidatorFunc(func(context.Context, proxy.AuthContext) error { close(entered); <-release; return nil })}, testAuthIdentity(time.Now().Add(time.Hour)).AuthContext(), func() { close(terminated) })
	done := make(chan error, 1)
	go func() { done <- a.check(context.Background(), true) }()
	<-entered
	select {
	case <-terminated:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("timeout waited for upstream return")
	}
	if err := a.checkLocal(); err == nil {
		t.Error("timed out connection can send")
	}
	close(release)
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("late success restored authentication", err)
	}
}
