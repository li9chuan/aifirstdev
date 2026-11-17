package account

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// commandFault interrupts one specific storage step before execution or after
// its real reply. The same scenarios run on miniredis and the real Cluster.
type commandFault struct {
	verb, key string
	pipeline  bool
	after     bool
	used      atomic.Bool
	action    func() error
	err       error
}

func installRedisFault(t *testing.T, s *RedisStore, fault *commandFault) {
	t.Helper()
	s.client.AddHook(fault)
	// Cluster Watch uses the selected primary's client, not the Cluster hook.
	if c, ok := s.client.(*redis.ClusterClient); ok {
		if err := c.ForEachMaster(context.Background(), func(_ context.Context, c *redis.Client) error {
			c.AddHook(fault)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *commandFault) DialHook(next redis.DialHook) redis.DialHook { return next }
func (f *commandFault) matches(cmd redis.Cmder) bool {
	return cmd.Name() == f.verb && len(cmd.Args()) > 1 && fmt.Sprint(cmd.Args()[1]) == f.key
}
func (f *commandFault) invoke(next func() error) error {
	if f.after {
		if err := next(); err != nil {
			return err
		}
	}
	if f.action != nil {
		if err := f.action(); err != nil {
			return err
		}
	}
	if f.err != nil {
		return f.err
	}
	if !f.after {
		return next()
	}
	return nil
}
func (f *commandFault) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if f.pipeline || !f.matches(cmd) || !f.used.CompareAndSwap(false, true) {
			return next(ctx, cmd)
		}
		return f.invoke(func() error { return next(ctx, cmd) })
	}
}
func (f *commandFault) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		match := false
		for _, cmd := range cmds {
			if f.matches(cmd) {
				match = true
				break
			}
		}
		if !f.pipeline || !match || !f.used.CompareAndSwap(false, true) {
			return next(ctx, cmds)
		}
		return f.invoke(func() error { return next(ctx, cmds) })
	}
}

func runRedisFaultContract(t *testing.T, factory func(*testing.T) *RedisStore) {
	t.Helper()
	ctx := context.Background()
	createActor := func(t *testing.T, s *RedisStore) (AccountSnapshot, LoginSession) {
		t.Helper()
		a, err := s.CreateAccount(ctx, testRef("a"))
		if err != nil {
			t.Fatal(err)
		}
		actor, err := s.OpenSession(ctx, OpenSessionRequest{Identity: testRef("a"), SessionID: strings.Repeat("a", 32), ExpiresAt: contractTime.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		return a, actor
	}
	for _, step := range []string{"sequence", "account", "index"} {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("registration-%s-after-%v", step, after), func(t *testing.T) {
				s := factory(t)
				fault := &commandFault{verb: "set", after: after, err: io.ErrUnexpectedEOF}
				switch step {
				case "sequence":
					fault.verb = "incr"
					fault.key = s.prefix + ":seq:act"
				case "account":
					fault.key = s.accountKey(1)
				case "index":
					fault.key = s.loginKey(testRef("a"))
				}
				installRedisFault(t, s, fault)
				if _, err := s.CreateAccount(ctx, testRef("a")); !errors.Is(err, ErrResultUnknown) || !fault.used.Load() {
					t.Fatalf("unknown=%v used=%v", err, fault.used.Load())
				}
				accountExists, err := s.client.Exists(ctx, s.accountKey(1)).Result()
				if err != nil {
					t.Fatal(err)
				}
				wantAccount := int64(0)
				if step == "index" || step == "account" && after {
					wantAccount = 1
				}
				if accountExists != wantAccount {
					t.Fatalf("candidate removed or unexpectedly written: %d", accountExists)
				}
				indexExists, err := s.client.Exists(ctx, s.loginKey(testRef("a"))).Result()
				if err != nil {
					t.Fatal(err)
				}
				wantIndex := int64(0)
				if step == "index" && after {
					wantIndex = 1
				}
				if indexExists != wantIndex {
					t.Fatal("unexpected publication", indexExists)
				}
			})
		}
	}
	for _, step := range []string{"index", "account"} {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("binding-%s-after-%v", step, after), func(t *testing.T) {
				s := factory(t)
				a, actor := createActor(t, s)
				fault := &commandFault{verb: "set", key: s.loginKey(testRef("b")), after: after, err: io.ErrUnexpectedEOF}
				if step == "account" {
					fault.key = s.accountKey(a.AccountID)
					fault.pipeline = true
				}
				installRedisFault(t, s, fault)
				req := IdentityWrite{Actor: actor.Ref, Reauthenticated: testRef("a"), Target: testRef("b"), ExpectedRevision: 1}
				if _, err := s.BindIdentity(ctx, req); !errors.Is(err, ErrResultUnknown) || !fault.used.Load() {
					t.Fatalf("bind=%v used=%v", err, fault.used.Load())
				}
				current, err := s.GetAccount(ctx, a.AccountID)
				if err != nil {
					t.Fatal(err)
				}
				committed := step == "account" && after
				if (identityIndex(current, testRef("b")) >= 0) != committed {
					t.Fatalf("membership=%+v", current)
				}
				if _, err = s.ValidateSession(ctx, actor.Ref); err != nil {
					t.Fatal("unrelated session invalid", err)
				}
				if step == "account" || after {
					owner, err := s.loadLoginAccountID(ctx, testRef("b"))
					if err != nil || owner != a.AccountID {
						t.Fatal("ambiguous reservation released", err)
					}
					_, err = s.BindIdentity(ctx, req)
					if committed {
						if !errors.Is(err, ErrIdentityRevisionConflict) {
							t.Fatal(err)
						}
					} else if !errors.Is(err, ErrUnavailable) {
						t.Fatal("adopted pending reservation", err)
					}
				}
			})
		}
	}
	for _, step := range []string{"account", "cleanup"} {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("unlink-%s-after-%v", step, after), func(t *testing.T) {
				s := factory(t)
				a, actor := createActor(t, s)
				req := IdentityWrite{Actor: actor.Ref, Reauthenticated: testRef("a"), Target: testRef("b"), ExpectedRevision: 1}
				if _, err := s.BindIdentity(ctx, req); err != nil {
					t.Fatal(err)
				}
				req.ExpectedRevision = 2
				fault := &commandFault{verb: "set", key: s.accountKey(a.AccountID), pipeline: true, after: after, err: io.ErrUnexpectedEOF}
				if step == "cleanup" {
					fault.verb = "del"
					fault.key = s.loginKey(testRef("b"))
				}
				installRedisFault(t, s, fault)
				if _, err := s.UnbindIdentity(ctx, req); !errors.Is(err, ErrResultUnknown) || !fault.used.Load() {
					t.Fatalf("unlink=%v used=%v", err, fault.used.Load())
				}
				current, err := s.GetAccount(ctx, a.AccountID)
				if err != nil {
					t.Fatal(err)
				}
				removed := step == "cleanup" || after
				if (identityIndex(current, testRef("b")) < 0) != removed {
					t.Fatal("membership rollback or unexpected change")
				}
				indexExists, err := s.client.Exists(ctx, s.loginKey(testRef("b"))).Result()
				if err != nil {
					t.Fatal(err)
				}
				if (indexExists == 0) != (step == "cleanup" && after) {
					t.Fatal("index changed without confirmed deletion")
				}
				if _, err = s.ValidateSession(ctx, actor.Ref); err != nil {
					t.Fatal("unlink invalidated session", err)
				}
				if removed {
					if _, err = s.UnbindIdentity(ctx, req); !errors.Is(err, ErrIdentityRevisionConflict) {
						t.Fatal("stale operation replayed cleanup", err)
					}
				}
			})
		}
	}
	for _, step := range []string{"session", "revoke", "epoch", "status"} {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-after-%v", step, after), func(t *testing.T) {
				s := factory(t)
				a, actor := createActor(t, s)
				id := strings.Repeat("b", 32)
				fault := &commandFault{verb: "set", key: s.sessionKey(id), after: after, err: io.ErrUnexpectedEOF}
				switch step {
				case "revoke":
					fault.verb = "del"
					fault.key = s.sessionKey(actor.Ref.SessionID)
				case "epoch", "status":
					fault.key = s.accountKey(a.AccountID)
					fault.pipeline = true
				}
				installRedisFault(t, s, fault)
				var err error
				switch step {
				case "session":
					_, err = s.OpenSession(ctx, OpenSessionRequest{Identity: testRef("a"), SessionID: id, ExpiresAt: contractTime.Add(time.Hour)})
				case "revoke":
					err = s.RevokeSession(ctx, actor.Ref)
				case "epoch":
					_, err = s.AdvanceSessionEpoch(ctx, a.AccountID, 1)
				case "status":
					_, err = s.ChangeAccountStatus(ctx, StatusWrite{AccountID: a.AccountID, Status: AccountStatusDisabled, ExpectedEpoch: 1})
				}
				if !errors.Is(err, ErrResultUnknown) || !fault.used.Load() {
					t.Fatal("unknown result not preserved", err)
				}
				switch step {
				case "session":
					n, err := s.client.Exists(ctx, s.sessionKey(id)).Result()
					if err != nil || (n == 1) != after {
						t.Fatal("session commit state", n, err)
					}
				case "revoke":
					_, err := s.ValidateSession(ctx, actor.Ref)
					if (err != nil) != after {
						t.Fatal("revocation state", err)
					}
					if err = s.RevokeSession(ctx, actor.Ref); err != nil {
						t.Fatal("idempotent delete", err)
					}
				case "epoch":
					if epoch, err := s.AdvanceSessionEpoch(ctx, a.AccountID, 1); err != nil || epoch != 2 {
						t.Fatal("epoch retry", epoch, err)
					}
					fresh, err := s.OpenSession(ctx, OpenSessionRequest{Identity: testRef("a"), SessionID: id, ExpiresAt: contractTime.Add(time.Hour)})
					if err != nil {
						t.Fatal(err)
					}
					if _, err = s.AdvanceSessionEpoch(ctx, a.AccountID, 1); err != nil {
						t.Fatal(err)
					}
					if _, err = s.ValidateSession(ctx, fresh.Ref); err != nil {
						t.Fatal("old retry revoked new login", err)
					}
				case "status":
					current, err := s.GetAccount(ctx, a.AccountID)
					if err != nil || (current.Status == AccountStatusDisabled) != after {
						t.Fatal("status", current, err)
					}
					if after {
						if _, err = s.ChangeAccountStatus(ctx, StatusWrite{AccountID: a.AccountID, Status: AccountStatusActive, ExpectedEpoch: 2}); err != nil {
							t.Fatal(err)
						}
						if _, err = s.ValidateSession(ctx, actor.Ref); !errors.Is(err, ErrSessionInvalid) {
							t.Fatal("reactivation restored old session", err)
						}
					}
				}
			})
		}
	}
	t.Run("pending-reservation-not-adopted", func(t *testing.T) {
		s := factory(t)
		_, actor := createActor(t, s)
		reserved, release := make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		defer releaseOnce.Do(func() { close(release) })
		fault := &commandFault{verb: "set", key: s.loginKey(testRef("b")), after: true, action: func() error {
			close(reserved)
			select {
			case <-release:
				return nil
			case <-time.After(5 * time.Second):
				return context.DeadlineExceeded
			}
		}}
		installRedisFault(t, s, fault)
		req := IdentityWrite{Actor: actor.Ref, Reauthenticated: testRef("a"), Target: testRef("b"), ExpectedRevision: 1}
		done := make(chan error, 1)
		go func() { _, err := s.BindIdentity(ctx, req); done <- err }()
		select {
		case <-reserved:
		case err := <-done:
			t.Fatal("reserve did not block", err)
		case <-time.After(5 * time.Second):
			t.Fatal("reservation timeout")
		}
		if _, err := s.BindIdentity(ctx, req); !errors.Is(err, ErrUnavailable) {
			t.Fatal("second operation adopted first reservation", err)
		}
		releaseOnce.Do(func() { close(release) })
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
	t.Run("old-cleanup-cannot-delete-rebind", func(t *testing.T) {
		s := factory(t)
		a, actor := createActor(t, s)
		req := IdentityWrite{Actor: actor.Ref, Reauthenticated: testRef("a"), Target: testRef("b"), ExpectedRevision: 1}
		if _, err := s.BindIdentity(ctx, req); err != nil {
			t.Fatal(err)
		}
		fault := &commandFault{verb: "del", key: s.loginKey(testRef("b")), pipeline: true, action: func() error {
			// Simulate another authorized recovery completing the old release and
			// a newly authenticated bind before this WATCH's queued DEL executes.
			if err := s.client.Del(ctx, s.loginKey(testRef("b"))).Err(); err != nil {
				return err
			}
			fresh := req
			fresh.ExpectedRevision = 3
			_, err := s.BindIdentity(ctx, fresh)
			return err
		}}
		installRedisFault(t, s, fault)
		req.ExpectedRevision = 2
		if _, err := s.UnbindIdentity(ctx, req); !errors.Is(err, ErrUnavailable) {
			t.Fatal("old cleanup retried", err)
		}
		current, err := s.ResolveAccount(ctx, testRef("b"))
		if err != nil || current.AccountID != a.AccountID || current.IdentityRevision != 4 {
			t.Fatalf("rebind lost: %+v %v", current, err)
		}
	})
	t.Run("late-old-epoch-session-is-never-upgraded", func(t *testing.T) {
		s := factory(t)
		a, _ := createActor(t, s)
		id := strings.Repeat("b", 32)
		fault := &commandFault{verb: "set", key: s.sessionKey(id), action: func() error { _, err := s.AdvanceSessionEpoch(ctx, a.AccountID, 1); return err }}
		installRedisFault(t, s, fault)
		if _, err := s.OpenSession(ctx, OpenSessionRequest{Identity: testRef("a"), SessionID: id, ExpiresAt: contractTime.Add(time.Hour)}); !errors.Is(err, ErrSessionInvalid) {
			t.Fatal("late session became valid", err)
		}
		fresh, err := s.OpenSession(ctx, OpenSessionRequest{Identity: testRef("a"), SessionID: strings.Repeat("c", 32), ExpiresAt: contractTime.Add(time.Hour)})
		if err != nil || fresh.SessionEpoch != 2 {
			t.Fatalf("fresh=%+v %v", fresh, err)
		}
	})
	t.Run("epoch-change-prevents-stale-identity-write", func(t *testing.T) {
		s := factory(t)
		a, actor := createActor(t, s)
		fault := &commandFault{verb: "set", key: s.accountKey(a.AccountID), pipeline: true, action: func() error { _, err := s.AdvanceSessionEpoch(ctx, a.AccountID, 1); return err }}
		installRedisFault(t, s, fault)
		if _, err := s.BindIdentity(ctx, IdentityWrite{Actor: actor.Ref, Reauthenticated: testRef("a"), Target: testRef("b"), ExpectedRevision: 1}); !errors.Is(err, ErrSessionInvalid) {
			t.Fatal(err)
		}
		current, err := s.GetAccount(ctx, a.AccountID)
		if err != nil || current.SessionEpoch != 2 || current.IdentityRevision != 1 {
			t.Fatalf("lost update: %+v %v", current, err)
		}
		if _, err = s.loadLoginAccountID(ctx, testRef("b")); !errors.Is(err, ErrAccountNotFound) {
			t.Fatal("definite rejection left owned reservation", err)
		}
	})
}

func TestRedisFaultContract(t *testing.T) {
	runRedisFaultContract(t, func(t *testing.T) *RedisStore { s, _ := newRedisTestStore(t); return s })
}

// readLossConn returns EOF after consuming the first response to the armed
// command, forcing the client's real retry loop to confront a lost success reply.
type readLossConn struct {
	net.Conn
	target string
	verb   string
	armed  *atomic.Bool
	lost   *atomic.Bool
	writes *atomic.Int32
	drop   bool
}

func (c *readLossConn) Write(p []byte) (int, error) {
	verb := c.verb
	if verb == "" {
		verb = "set"
	}
	if c.armed.Load() && strings.Contains(string(p), c.target) && strings.Contains(strings.ToLower(string(p)), "\r\n$"+fmt.Sprint(len(verb))+"\r\n"+verb+"\r\n") {
		c.writes.Add(1)
		c.drop = c.lost.CompareAndSwap(false, true)
	}
	return c.Conn.Write(p)
}
func (c *readLossConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if c.drop && n > 0 {
		c.drop = false
		_ = c.Conn.Close()
		return 0, io.ErrUnexpectedEOF
	}
	return n, err
}
