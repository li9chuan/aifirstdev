package account

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-account/credential"
)

func TestResolveAccountIsReadOnly(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore(MemoryOptions{Now: func() time.Time { return contractTime }})
	s := newTestService(t, store)
	p := credential.Principal{AuthorityID: ProviderDemo, Subject: "alice"}
	if _, err := s.ResolveAccount(ctx, p); !errors.Is(err, ErrAccountNotFound) {
		t.Fatal(err)
	}
	if len(store.accounts) != 0 || len(store.sessions) != 0 || store.nextAccountID != 1000 {
		t.Fatal("lookup mutated state")
	}
	a, err := s.Register(ctx, RegistrationRequest{First: DemoCredential("alice")})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := LoginRefFromPrincipal(s.opts.IndexKey, p)
	if err != nil || ref != a.Identities[0] {
		t.Fatal(ref, err)
	}
	for i := 0; i < 3; i++ {
		got, err := s.ResolveAccount(ctx, p)
		if err != nil || got.AccountID != a.AccountID {
			t.Fatal(got, err)
		}
	}
	if len(store.sessions) != 0 || store.nextAccountID != a.AccountID {
		t.Fatal("lookup created session/account")
	}
	if _, err := s.ChangeAccountStatus(ctx, StatusWrite{AccountID: a.AccountID, ExpectedEpoch: 1, Status: AccountStatusDisabled}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ResolveAccount(ctx, p)
	if err != nil || got.Status != AccountStatusDisabled {
		t.Fatal("disabled account hidden", got, err)
	}
	if _, err := LoginRefFromPrincipal([]byte("short"), p); !errors.Is(err, ErrInvalidCredential) {
		t.Fatal("weak index key accepted", err)
	}
}

type maintenanceStore interface {
	Store
	CreatedAccountCleaner
}

func runMaintenanceContract(t *testing.T, factory func(*testing.T) maintenanceStore) {
	ctx := context.Background()
	t.Run("remove-and-repeat", func(t *testing.T) {
		s := factory(t)
		ref := testRef("imported")
		a, err := s.CreateAccount(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		session, err := s.OpenSession(ctx, OpenSessionRequest{Identity: ref, SessionID: strings.Repeat("a", 32), ExpiresAt: contractTime.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := s.DeleteCreatedAccount(ctx, testRef("other"), a.AccountID); err == nil || ok {
			t.Fatal("wrong identity deleted account", ok, err)
		}
		if _, err := s.GetAccount(ctx, a.AccountID); err != nil {
			t.Fatal(err)
		}
		if ok, err := s.DeleteCreatedAccount(ctx, ref, a.AccountID); err != nil || !ok {
			t.Fatal(ok, err)
		}
		if _, err := s.ResolveAccount(ctx, ref); !errors.Is(err, ErrAccountNotFound) {
			t.Fatal(err)
		}
		if _, err := s.ValidateSession(ctx, session.Ref); err == nil {
			t.Fatal("residual session accepted")
		}
		if ok, err := s.DeleteCreatedAccount(ctx, ref, a.AccountID); err != nil || ok {
			t.Fatal(ok, err)
		}
		next, err := s.CreateAccount(ctx, ref)
		if err != nil || next.AccountID <= a.AccountID {
			t.Fatal("ID reused", next, err)
		}
		if ok, err := s.DeleteCreatedAccount(ctx, ref, a.AccountID); err == nil || ok {
			t.Fatal("new owner's index removed", ok, err)
		}
		if _, err := s.ResolveAccount(ctx, ref); err != nil {
			t.Fatal(err)
		}
	})
	for _, change := range []string{"epoch", "status", "identity"} {
		t.Run("reject-changed-"+change, func(t *testing.T) {
			s := factory(t)
			ref := testRef("imported")
			a, err := s.CreateAccount(ctx, ref)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "epoch":
				_, err = s.AdvanceSessionEpoch(ctx, a.AccountID, 1)
			case "status":
				_, err = s.ChangeAccountStatus(ctx, StatusWrite{AccountID: a.AccountID, ExpectedEpoch: 1, Status: AccountStatusDisabled})
			case "identity":
				var session LoginSession
				session, err = s.OpenSession(ctx, OpenSessionRequest{Identity: ref, SessionID: strings.Repeat("b", 32), ExpiresAt: contractTime.Add(time.Hour)})
				if err == nil {
					_, err = s.BindIdentity(ctx, IdentityWrite{Actor: session.Ref, Reauthenticated: ref, Target: testRef("second"), ExpectedRevision: 1})
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if ok, err := s.DeleteCreatedAccount(ctx, ref, a.AccountID); !errors.Is(err, ErrIdentityRevisionConflict) || ok {
				t.Fatal(ok, err)
			}
			if _, err := s.GetAccount(ctx, a.AccountID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMemoryMaintenance(t *testing.T) {
	runMaintenanceContract(t, func(t *testing.T) maintenanceStore {
		return NewMemoryStore(MemoryOptions{Now: func() time.Time { return contractTime }})
	})
}
func TestRedisMaintenance(t *testing.T) {
	runMaintenanceContract(t, func(t *testing.T) maintenanceStore { s, _ := newRedisTestStore(t); return s })
}
func TestRealRedisMaintenance(t *testing.T) {
	for _, cluster := range []bool{false, true} {
		t.Run(fmt.Sprintf("cluster-%v", cluster), func(t *testing.T) {
			runMaintenanceContract(t, func(t *testing.T) maintenanceStore { return realRedisStore(t, cluster) })
		})
	}
}

func runMaintenanceFaults(t *testing.T, factory func(*testing.T) *RedisStore) {
	ctx := context.Background()
	for _, step := range []string{"account", "index"} {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-after-%v", step, after), func(t *testing.T) {
				s := factory(t)
				ref := testRef("imported")
				a, err := s.CreateAccount(ctx, ref)
				if err != nil {
					t.Fatal(err)
				}
				key := s.accountKey(a.AccountID)
				if step == "index" {
					key = s.loginKey(ref)
				}
				fault := &commandFault{verb: "del", key: key, pipeline: true, after: after, err: io.ErrUnexpectedEOF}
				installRedisFault(t, s, fault)
				if ok, err := s.DeleteCreatedAccount(ctx, ref, a.AccountID); !errors.Is(err, ErrResultUnknown) || ok || !fault.used.Load() {
					t.Fatal(ok, err)
				}
				// Explicit offline recovery after inspecting the result; never an automatic retry.
				_, accountErr := s.GetAccount(ctx, a.AccountID)
				if step == "index" || after {
					if !errors.Is(accountErr, ErrAccountNotFound) {
						t.Fatal(accountErr)
					}
				} else if accountErr != nil {
					t.Fatal(accountErr)
				}
				if _, err := s.DeleteCreatedAccount(ctx, ref, a.AccountID); err != nil {
					t.Fatal(err)
				}
				if _, err := s.ResolveAccount(ctx, ref); !errors.Is(err, ErrAccountNotFound) {
					t.Fatal(err)
				}
			})
		}
	}
}
func TestRedisMaintenanceFaults(t *testing.T) {
	runMaintenanceFaults(t, func(t *testing.T) *RedisStore { s, _ := newRedisTestStore(t); return s })
}
func TestRealRedisMaintenanceFaults(t *testing.T) {
	runMaintenanceFaults(t, func(t *testing.T) *RedisStore { return realRedisStore(t, true) })
}

func TestRealRedisMaintenanceLostReply(t *testing.T) {
	for _, step := range []string{"account", "index"} {
		t.Run(step, func(t *testing.T) {
			ctx := context.Background()
			control := realRedisStore(t, true)
			ref := testRef("imported")
			a, err := control.CreateAccount(ctx, ref)
			if err != nil {
				t.Fatal(err)
			}
			target := control.accountKey(a.AccountID)
			if step == "index" {
				target = control.loginKey(ref)
			}
			var armed, lost atomic.Bool
			var writes atomic.Int32
			client := realRedisClient(t, true, func(ctx context.Context, network, addr string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				return &readLossConn{Conn: conn, target: target, verb: "del", armed: &armed, lost: &lost, writes: &writes}, nil
			})
			s, err := NewRedisStore(client, RedisStoreOptions{Space: strings.TrimPrefix(control.prefix, "account:"), Now: func() time.Time { return contractTime }})
			if err != nil {
				t.Fatal(err)
			}
			armed.Store(true)
			_, err = s.DeleteCreatedAccount(ctx, ref, a.AccountID)
			armed.Store(false)
			if !errors.Is(err, ErrResultUnknown) || !lost.Load() || writes.Load() != 1 {
				t.Fatalf("cleanup replay: %v lost=%v writes=%d", err, lost.Load(), writes.Load())
			}
			if _, err := control.GetAccount(ctx, a.AccountID); !errors.Is(err, ErrAccountNotFound) {
				t.Fatal("account delete did not commit", err)
			}
			if _, err := control.DeleteCreatedAccount(ctx, ref, a.AccountID); err != nil {
				t.Fatal(err)
			}
			if _, err := control.ResolveAccount(ctx, ref); !errors.Is(err, ErrAccountNotFound) {
				t.Fatal(err)
			}
		})
	}
}
