package account

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-account/credential"
	"github.com/li9chuan/aifirstdev/aifd-account/generated/accountpb"
	"google.golang.org/protobuf/proto"
)

var contractTime = time.Unix(1800000000, 0)

func newTestService(t *testing.T, store Store, change ...func(*Options)) *Service {
	t.Helper()
	opts := Options{BackendID: "test", Audience: "business", IndexKey: []byte("separate-test-index-key-32-bytes!!"), SessionTTL: time.Hour, TicketTTL: time.Minute, Now: func() time.Time { return contractTime }, RegistrationPolicy: RegistrationPolicyFunc(func(context.Context, credential.Principal, []byte) error { return nil })}
	for _, fn := range change {
		fn(&opts)
	}
	issuer := TokenIssuerFunc(func(_ context.Context, p AuthPayload) (LoginTicket, error) {
		raw, err := proto.Marshal(&accountpb.LoginTicketPayload{BackendId: p.BackendID, Audience: p.Audience, Session: &accountpb.LoginSessionRef{AccountId: p.Session.AccountID, SessionId: p.Session.SessionID}, IssuedAtUnix: p.IssuedAtUnix, ExpiresAtUnix: p.ExpiresAtUnix})
		return LoginTicket{Payload: raw, Token: "test-signature"}, err
	})
	s, err := NewService(store, NewDemoVerifier(), issuer, opts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func ticketSession(t *testing.T, result LoginResult) SessionRef {
	t.Helper()
	var p accountpb.LoginTicketPayload
	if err := proto.Unmarshal(result.Ticket.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.BackendId != "test" || p.Audience != "business" || p.GetSession().GetAccountId() != result.AccountID || result.Ticket.Token == "" {
		t.Fatalf("bad ticket: %+v", &p)
	}
	return SessionRef{AccountID: p.Session.AccountId, SessionID: p.Session.SessionId}
}

func TestServiceExplicitRegistrationAndIdentityLifecycle(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore(MemoryOptions{Now: func() time.Time { return contractTime }})
	s := newTestService(t, store)
	if _, err := s.Login(ctx, DemoCredential("alice")); !errors.Is(err, ErrAccountNotFound) {
		t.Fatal(err)
	}
	if len(store.accounts) != 0 {
		t.Fatal("login implicitly created account")
	}
	a, err := s.Register(ctx, RegistrationRequest{First: DemoCredential("alice")})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.sessions) != 0 {
		t.Fatal("register opened a session")
	}
	if _, err := s.Register(ctx, RegistrationRequest{First: DemoCredential("alice")}); !errors.Is(err, ErrIdentityInUse) {
		t.Fatal(err)
	}
	login, err := s.Login(ctx, DemoCredential("alice"))
	if err != nil {
		t.Fatal(err)
	}
	actor := ticketSession(t, login)
	change := IdentityChange{Actor: actor, Reauth: DemoCredential("alice"), ExpectedRevision: a.IdentityRevision}
	rev, err := s.BindIdentity(ctx, change, DemoCredential("alternate"))
	if err != nil || rev != 2 {
		t.Fatalf("revision=%d err=%v", rev, err)
	}
	if _, err := s.BindIdentity(ctx, change, DemoCredential("alternate")); !errors.Is(err, ErrIdentityRevisionConflict) {
		t.Fatal(err)
	}
	change.ExpectedRevision = rev
	if same, err := s.BindIdentity(ctx, change, DemoCredential("alternate")); err != nil || same != rev {
		t.Fatalf("no-op=%d %v", same, err)
	}
	alternate, err := s.Login(ctx, DemoCredential("alternate"))
	if err != nil || alternate.AccountID != a.AccountID {
		t.Fatalf("alternate=%+v %v", alternate, err)
	}
	change.Reauth = DemoCredential("alternate")
	if rev, err = s.UnbindIdentity(ctx, change, a.Identities[0]); err != nil || rev != 3 {
		t.Fatalf("unbind=%d %v", rev, err)
	}
	if _, err := s.Login(ctx, DemoCredential("alice")); !errors.Is(err, ErrAccountNotFound) {
		t.Fatal(err)
	}
	if _, err := s.ValidateSession(ctx, actor); err != nil {
		t.Fatalf("unlink revoked old session: %v", err)
	}
	current, err := s.GetAccount(ctx, a.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	change.ExpectedRevision = current.IdentityRevision
	if _, err := s.UnbindIdentity(ctx, change, current.Identities[0]); !errors.Is(err, ErrLastLoginIdentity) {
		t.Fatal(err)
	}
	if err := s.RevokeSession(ctx, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ValidateSession(ctx, actor); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal(err)
	}
	if _, err := s.ValidateSession(ctx, ticketSession(t, alternate)); err != nil {
		t.Fatalf("revoked unrelated session: %v", err)
	}
}

func TestRegistrationPolicyAndInvalidCredentialsDoNotWrite(t *testing.T) {
	store := NewMemoryStore(MemoryOptions{})
	s := newTestService(t, store, func(o *Options) { o.RegistrationPolicy = nil })
	if _, err := s.Register(context.Background(), RegistrationRequest{First: DemoCredential("blocked")}); !errors.Is(err, ErrRegistrationDenied) {
		t.Fatal(err)
	}
	if len(store.accounts) != 0 {
		t.Fatal("denied registration wrote an account")
	}
	s = newTestService(t, store)
	proof := DemoCredential("alice")
	proof.Proof = []byte(`{"Subject":"alice","Secret":"wrong"}`)
	if _, err := s.Register(context.Background(), RegistrationRequest{First: proof}); !errors.Is(err, ErrInvalidCredential) {
		t.Fatal(err)
	}
	if len(store.accounts) != 0 {
		t.Fatal("invalid proof wrote an account")
	}
}

func TestVerificationBindingAndIndexIsolation(t *testing.T) {
	s := newTestService(t, NewMemoryStore(MemoryOptions{}))
	base := VerificationContext{Purpose: VerifyBindCurrent, Actor: SessionRef{AccountID: 1, SessionID: fmt.Sprintf("%032x", 1)}, ExpectedRevision: 1}
	one, err := s.BuildVerificationBinding(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*VerificationContext){func(v *VerificationContext) { v.Actor.AccountID++ }, func(v *VerificationContext) { v.Actor.SessionID = fmt.Sprintf("%032x", 2) }, func(v *VerificationContext) { v.ExpectedRevision++ }, func(v *VerificationContext) { v.Purpose = VerifyBindCandidate }} {
		v := base
		mutate(&v)
		other, err := s.BuildVerificationBinding(v)
		if err != nil || other == one {
			t.Fatalf("binding not scoped: %+v %v", v, err)
		}
	}
	a, _ := loginRefFromPrincipal(s.opts.IndexKey, credential.Principal{AuthorityID: "issuer-a", Subject: "same"})
	b, _ := loginRefFromPrincipal(s.opts.IndexKey, credential.Principal{AuthorityID: "issuer-b", Subject: "same"})
	c, _ := loginRefFromPrincipal([]byte("another-index-key-with-32-bytes!!"), credential.Principal{AuthorityID: "issuer-a", Subject: "same"})
	if a.SubjectDigest == b.SubjectDigest || a.SubjectDigest == c.SubjectDigest {
		t.Fatal("authority/key isolation lost")
	}
}

func TestRenewalDoesNotExtendSession(t *testing.T) {
	now := contractTime
	store := NewMemoryStore(MemoryOptions{Now: func() time.Time { return now }})
	s := newTestService(t, store, func(o *Options) { o.Now = func() time.Time { return now }; o.SessionTTL = 2 * time.Minute })
	if _, err := s.Register(context.Background(), RegistrationRequest{First: DemoCredential("alice")}); err != nil {
		t.Fatal(err)
	}
	login, err := s.Login(context.Background(), DemoCredential("alice"))
	if err != nil {
		t.Fatal(err)
	}
	ref := ticketSession(t, login)
	now = now.Add(90 * time.Second)
	ticket, err := s.IssueLoginTicket(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	var p accountpb.LoginTicketPayload
	if err := proto.Unmarshal(ticket.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.ExpiresAtUnix != contractTime.Add(2*time.Minute).Unix() || p.Session.SessionId != ref.SessionID {
		t.Fatal("renewal extended or replaced session")
	}
	now = contractTime.Add(2 * time.Minute)
	if _, err := s.IssueLoginTicket(context.Background(), ref); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal(err)
	}
}

func testRef(name string) LoginRef {
	return LoginRef{AuthorityID: "test", SubjectDigest: sha256.Sum256([]byte(name))}
}

// The same behavioral contract runs against MemoryStore, standalone Redis and
// the real Cluster fixture; it asserts outcomes rather than mirroring CAS code.
func runStoreContract(t *testing.T, makeStore func(*testing.T) Store) {
	t.Helper()
	ctx := context.Background()
	create := func(t *testing.T, s Store, name string) AccountSnapshot {
		t.Helper()
		a, err := s.CreateAccount(ctx, testRef(name))
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	open := func(t *testing.T, s Store, name string, n int) LoginSession {
		t.Helper()
		session, err := s.OpenSession(ctx, OpenSessionRequest{Identity: testRef(name), SessionID: fmt.Sprintf("%032x", n), ExpiresAt: contractTime.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	t.Run("registration-uniqueness", func(t *testing.T) {
		s := makeStore(t)
		var winners atomic.Int32
		var wg sync.WaitGroup
		for range 12 {
			wg.Go(func() {
				_, err := s.CreateAccount(ctx, testRef("contended"))
				if err == nil {
					winners.Add(1)
				} else if !errors.Is(err, ErrIdentityInUse) {
					t.Errorf("register: %v", err)
				}
			})
		}
		wg.Wait()
		if winners.Load() != 1 {
			t.Fatalf("winners=%d", winners.Load())
		}
		if _, err := s.ResolveAccount(ctx, testRef("missing")); !errors.Is(err, ErrAccountNotFound) {
			t.Fatal(err)
		}
	})
	t.Run("binding-ownership-and-unlink", func(t *testing.T) {
		s := makeStore(t)
		a := create(t, s, "a")
		b := create(t, s, "b")
		actor := open(t, s, "a", 1)
		req := IdentityWrite{Actor: actor.Ref, Reauthenticated: testRef("b"), Target: testRef("c"), ExpectedRevision: 1}
		if _, err := s.BindIdentity(ctx, req); !errors.Is(err, ErrInvalidCredential) {
			t.Fatal(err)
		}
		req.Reauthenticated = testRef("a")
		req.Target = testRef("b")
		if _, err := s.BindIdentity(ctx, req); !errors.Is(err, ErrIdentityInUse) {
			t.Fatal(err)
		}
		req.Target = testRef("c")
		rev, err := s.BindIdentity(ctx, req)
		if err != nil || rev != 2 {
			t.Fatalf("bind=%d %v", rev, err)
		}
		if _, err = s.BindIdentity(ctx, req); !errors.Is(err, ErrIdentityRevisionConflict) {
			t.Fatal(err)
		}
		req.ExpectedRevision = rev
		if same, err := s.BindIdentity(ctx, req); err != nil || same != rev {
			t.Fatalf("repeat=%d %v", same, err)
		}
		other := open(t, s, "c", 2)
		if other.Ref.AccountID != a.AccountID {
			t.Fatal("binding created a new account")
		}
		req.Target = testRef("a")
		req.Reauthenticated = testRef("c")
		rev, err = s.UnbindIdentity(ctx, req)
		if err != nil || rev != 3 {
			t.Fatalf("unlink=%d %v", rev, err)
		}
		if _, err = s.ValidateSession(ctx, actor.Ref); err != nil {
			t.Fatal("unlink revoked session", err)
		}
		if _, err = s.ResolveAccount(ctx, testRef("a")); !errors.Is(err, ErrAccountNotFound) {
			t.Fatal(err)
		}
		req.Target = testRef("c")
		req.ExpectedRevision = rev
		if _, err = s.UnbindIdentity(ctx, req); !errors.Is(err, ErrLastLoginIdentity) {
			t.Fatal(err)
		}
		if _, err = s.ValidateSession(ctx, SessionRef{AccountID: b.AccountID, SessionID: actor.Ref.SessionID}); !errors.Is(err, ErrSessionInvalid) {
			t.Fatal(err)
		}
		if err = s.RevokeSession(ctx, SessionRef{AccountID: b.AccountID, SessionID: actor.Ref.SessionID}); !errors.Is(err, ErrSessionInvalid) {
			t.Fatal(err)
		}
		if _, err = s.ValidateSession(ctx, actor.Ref); err != nil {
			t.Fatal("wrong-owner revoke removed session", err)
		}
	})
	t.Run("single-and-all-revocation", func(t *testing.T) {
		s := makeStore(t)
		a := create(t, s, "a")
		one := open(t, s, "a", 1)
		two := open(t, s, "a", 2)
		if err := s.RevokeSession(ctx, one.Ref); err != nil {
			t.Fatal(err)
		}
		if err := s.RevokeSession(ctx, one.Ref); err != nil {
			t.Fatal("idempotent revoke", err)
		}
		if _, err := s.ValidateSession(ctx, one.Ref); !errors.Is(err, ErrSessionInvalid) {
			t.Fatal(err)
		}
		if _, err := s.ValidateSession(ctx, two.Ref); err != nil {
			t.Fatal(err)
		}
		epoch, err := s.AdvanceSessionEpoch(ctx, a.AccountID, 1)
		if err != nil || epoch != 2 {
			t.Fatalf("epoch=%d %v", epoch, err)
		}
		if _, err = s.ValidateSession(ctx, two.Ref); !errors.Is(err, ErrSessionInvalid) {
			t.Fatal(err)
		}
		fresh := open(t, s, "a", 3)
		if got, err := s.AdvanceSessionEpoch(ctx, a.AccountID, 1); err != nil || got != 2 {
			t.Fatalf("replay=%d %v", got, err)
		}
		if _, err = s.ValidateSession(ctx, fresh.Ref); err != nil {
			t.Fatal("retry revoked later login", err)
		}
		if _, err = s.AdvanceSessionEpoch(ctx, a.AccountID, 3); !errors.Is(err, ErrEpochConflict) {
			t.Fatal(err)
		}
	})
	t.Run("disable-enable-keeps-old-sessions-invalid", func(t *testing.T) {
		s := makeStore(t)
		a := create(t, s, "a")
		session := open(t, s, "a", 1)
		disabled, err := s.ChangeAccountStatus(ctx, StatusWrite{AccountID: a.AccountID, ExpectedEpoch: 1, Status: AccountStatusDisabled})
		if err != nil || disabled.SessionEpoch != 2 {
			t.Fatalf("disable=%+v %v", disabled, err)
		}
		if _, err = s.OpenSession(ctx, OpenSessionRequest{Identity: testRef("a"), SessionID: fmt.Sprintf("%032x", 2), ExpiresAt: contractTime.Add(time.Hour)}); !errors.Is(err, ErrAccountDisabled) {
			t.Fatal(err)
		}
		if _, err = s.ChangeAccountStatus(ctx, StatusWrite{AccountID: a.AccountID, ExpectedEpoch: 1, Status: AccountStatusActive}); !errors.Is(err, ErrEpochConflict) {
			t.Fatal(err)
		}
		enabled, err := s.ChangeAccountStatus(ctx, StatusWrite{AccountID: a.AccountID, ExpectedEpoch: 2, Status: AccountStatusActive})
		if err != nil || enabled.SessionEpoch != 3 {
			t.Fatalf("enable=%+v %v", enabled, err)
		}
		if _, err = s.ValidateSession(ctx, session.Ref); !errors.Is(err, ErrSessionInvalid) {
			t.Fatal("old session revived", err)
		}
	})
	t.Run("concurrent-unlink-retains-one-identity", func(t *testing.T) {
		s := makeStore(t)
		a := create(t, s, "a")
		actor := open(t, s, "a", 1)
		if _, err := s.BindIdentity(ctx, IdentityWrite{Actor: actor.Ref, Reauthenticated: testRef("a"), Target: testRef("b"), ExpectedRevision: 1}); err != nil {
			t.Fatal(err)
		}
		var successes atomic.Int32
		var wg sync.WaitGroup
		for _, target := range []LoginRef{testRef("a"), testRef("b")} {
			wg.Go(func() {
				_, err := s.UnbindIdentity(ctx, IdentityWrite{Actor: actor.Ref, Reauthenticated: testRef("a"), Target: target, ExpectedRevision: 2})
				if err == nil {
					successes.Add(1)
				} else if !errors.Is(err, ErrIdentityRevisionConflict) && !errors.Is(err, ErrInvalidCredential) && !errors.Is(err, ErrAccountNotFound) {
					t.Errorf("unlink=%v", err)
				}
			})
		}
		wg.Wait()
		current, err := s.GetAccount(ctx, a.AccountID)
		if err != nil || len(current.Identities) != 1 || successes.Load() != 1 {
			t.Fatalf("current=%+v successes=%d err=%v", current, successes.Load(), err)
		}
	})
}

func TestMemoryStoreContract(t *testing.T) {
	runStoreContract(t, func(t *testing.T) Store {
		return NewMemoryStore(MemoryOptions{Now: func() time.Time { return contractTime }})
	})
}
