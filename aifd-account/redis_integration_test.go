package account

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func realRedisStore(t *testing.T, cluster bool) *RedisStore {
	t.Helper()
	client := realRedisClient(t, cluster, nil)
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	space := "acceptance-" + hex.EncodeToString(suffix[:])
	s, err := NewRedisStore(client, RedisStoreOptions{Space: space, Now: func() time.Time { return contractTime }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		clean := func(ctx context.Context, c *redis.Client) error {
			var cursor uint64
			for {
				keys, next, err := c.Scan(ctx, cursor, s.prefix+":*", 100).Result()
				if err != nil {
					return err
				}
				for _, key := range keys {
					if !strings.HasPrefix(key, s.prefix+":") {
						return fmt.Errorf("cleanup namespace mismatch")
					}
					if err = c.Del(ctx, key).Err(); err != nil {
						return err
					}
				}
				cursor = next
				if cursor == 0 {
					return nil
				}
			}
		}
		if c, ok := client.(*redis.ClusterClient); ok {
			err = c.ForEachMaster(ctx, clean)
		} else {
			err = clean(ctx, client.(*redis.Client))
		}
		if err != nil {
			t.Errorf("test namespace cleanup: %v", err)
		}
	})
	return s
}

func realRedisClient(t *testing.T, cluster bool, dialer func(context.Context, string, string) (net.Conn, error)) redis.UniversalClient {
	t.Helper()
	var client redis.UniversalClient
	if cluster {
		addrs := os.Getenv("AIFD_ACCOUNT_REDIS_CLUSTER_ADDRS")
		if addrs == "" {
			t.Skip("AIFD_ACCOUNT_REDIS_CLUSTER_ADDRS unset: real Cluster not verified")
		}
		client = redis.NewClusterClient(&redis.ClusterOptions{Addrs: strings.Split(addrs, ","), Protocol: 2, DisableIdentity: true, Dialer: dialer, ReadTimeout: time.Second, WriteTimeout: time.Second, ContextTimeoutEnabled: true, MaxRetries: 3})
	} else {
		addr := os.Getenv("AIFD_ACCOUNT_REDIS_ADDR")
		if addr == "" {
			t.Skip("AIFD_ACCOUNT_REDIS_ADDR unset: real standalone Redis not verified")
		}
		client = redis.NewClient(&redis.Options{Addr: addr, Protocol: 2, DisableIdentity: true, Dialer: dialer, ReadTimeout: time.Second, WriteTimeout: time.Second, ContextTimeoutEnabled: true, MaxRetries: 3})
	}
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal("real Redis unavailable", err)
	}
	return client
}

func TestRealRedisStoreContract(t *testing.T) {
	runStoreContract(t, func(t *testing.T) Store { return realRedisStore(t, false) })
}
func TestRealRedisClusterContract(t *testing.T) {
	runStoreContract(t, func(t *testing.T) Store { return realRedisStore(t, true) })
}
func TestRealRedisClusterFaultContract(t *testing.T) {
	runRedisFaultContract(t, func(t *testing.T) *RedisStore { return realRedisStore(t, true) })
}

func TestRealRedisClusterUsesDifferentSlotsAndPrimaries(t *testing.T) {
	s := realRedisStore(t, true)
	ctx := context.Background()
	c := s.client.(*redis.ClusterClient)
	a, err := s.CreateAccount(ctx, testRef("a"))
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 32)
	if _, err = s.OpenSession(ctx, OpenSessionRequest{Identity: testRef("a"), SessionID: id, ExpiresAt: contractTime.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	ranges, err := c.ClusterSlots(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	slots, primaries := map[int64]bool{}, map[string]bool{}
	for _, key := range []string{s.prefix + ":seq:act", s.accountKey(a.AccountID), s.loginKey(testRef("a")), s.sessionKey(id)} {
		slot, err := c.ClusterKeySlot(ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
		if slots[slot] {
			t.Fatal("fixture did not span distinct slots", slot)
		}
		slots[slot] = true
		for _, r := range ranges {
			if slot >= int64(r.Start) && slot <= int64(r.End) {
				primaries[r.Nodes[0].Addr] = true
				t.Logf("key=%s slot=%d primary=%s", key, slot, r.Nodes[0].Addr)
			}
		}
	}
	if len(primaries) < 2 {
		t.Fatal("fixture did not cross primary nodes")
	}
}

func TestRealRedisExpirationAndAbsoluteDeadline(t *testing.T) {
	s := realRedisStore(t, true)
	s.now = time.Now
	ctx := context.Background()
	a, err := s.CreateAccount(ctx, testRef("a"))
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 32)
	session, err := s.OpenSession(ctx, OpenSessionRequest{Identity: testRef("a"), SessionID: id, ExpiresAt: time.Now().Add(2200 * time.Millisecond)})
	if err != nil {
		t.Fatal(err)
	}
	ttl, err := s.client.PTTL(ctx, s.sessionKey(id)).Result()
	if err != nil || ttl <= 0 || ttl > 2200*time.Millisecond {
		t.Fatalf("ttl=%v %v", ttl, err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		n, err := s.client.Exists(ctx, s.sessionKey(id)).Result()
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if n, err := s.client.Exists(ctx, s.sessionKey(id)).Result(); err != nil || n != 0 {
		t.Fatalf("TTL did not reclaim session: %d %v", n, err)
	}
	if _, err = s.ValidateSession(ctx, session.Ref); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal(err)
	}
	// A retained key still cannot override the stored absolute deadline.
	raw, err := marshalSession(LoginSession{Ref: SessionRef{AccountID: a.AccountID, SessionID: id}, SessionEpoch: 1, ExpiresAt: time.Now().Add(-time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.client.Set(ctx, s.sessionKey(id), raw, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ValidateSession(ctx, session.Ref); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal("TTL overrode deadline", err)
	}
}

func TestRealRedisLostReplyDoesNotReplayIndexWrite(t *testing.T) {
	for _, cluster := range []bool{false, true} {
		t.Run(fmt.Sprintf("cluster-%v", cluster), func(t *testing.T) {
			control := realRedisStore(t, cluster)
			var armed, lost atomic.Bool
			var writes atomic.Int32
			target := control.loginKey(testRef("new"))
			client := realRedisClient(t, cluster, func(ctx context.Context, network, addr string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				return &readLossConn{Conn: conn, target: target, armed: &armed, lost: &lost, writes: &writes}, nil
			})
			s, err := NewRedisStore(client, RedisStoreOptions{Space: strings.TrimPrefix(control.prefix, "account:"), Now: func() time.Time { return contractTime }})
			if err != nil {
				t.Fatal(err)
			}
			armed.Store(true)
			if _, err = s.CreateAccount(context.Background(), testRef("new")); !errors.Is(err, ErrResultUnknown) {
				t.Fatalf("lost reply not reported: %v", err)
			}
			armed.Store(false)
			if !lost.Load() || writes.Load() != 1 {
				t.Fatalf("conditional write replayed: lost=%v writes=%d", lost.Load(), writes.Load())
			}
			if _, err = control.ResolveAccount(context.Background(), testRef("new")); err != nil {
				t.Fatal("published identity was deleted after lost reply", err)
			}
		})
	}
}

func TestRealRedisLostTransactionReplyDoesNotReplay(t *testing.T) {
	for _, verb := range []string{"set", "del"} {
		t.Run(verb, func(t *testing.T) {
			control := realRedisStore(t, true)
			ctx := context.Background()
			a, err := control.CreateAccount(ctx, testRef("a"))
			if err != nil {
				t.Fatal(err)
			}
			actor, err := control.OpenSession(ctx, OpenSessionRequest{Identity: testRef("a"), SessionID: strings.Repeat("a", 32), ExpiresAt: contractTime.Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			req := IdentityWrite{Actor: actor.Ref, Reauthenticated: testRef("a"), Target: testRef("b"), ExpectedRevision: 1}
			target := control.accountKey(a.AccountID)
			if verb == "del" {
				if _, err = control.BindIdentity(ctx, req); err != nil {
					t.Fatal(err)
				}
				req.ExpectedRevision = 2
				target = control.loginKey(testRef("b"))
			}
			var armed, lost atomic.Bool
			var writes atomic.Int32
			client := realRedisClient(t, true, func(ctx context.Context, network, addr string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				return &readLossConn{Conn: conn, target: target, verb: verb, armed: &armed, lost: &lost, writes: &writes}, nil
			})
			s, err := NewRedisStore(client, RedisStoreOptions{Space: strings.TrimPrefix(control.prefix, "account:"), Now: func() time.Time { return contractTime }})
			if err != nil {
				t.Fatal(err)
			}
			armed.Store(true)
			if verb == "set" {
				_, err = s.BindIdentity(ctx, req)
			} else {
				_, err = s.UnbindIdentity(ctx, req)
			}
			armed.Store(false)
			if !errors.Is(err, ErrResultUnknown) || !lost.Load() || writes.Load() != 1 {
				t.Fatalf("transaction retry: err=%v lost=%v writes=%d", err, lost.Load(), writes.Load())
			}
			current, err := control.GetAccount(ctx, a.AccountID)
			if err != nil {
				t.Fatal(err)
			}
			if verb == "set" {
				if current.IdentityRevision != 2 || identityIndex(current, testRef("b")) < 0 {
					t.Fatal("committed binding lost", current)
				}
			} else {
				if current.IdentityRevision != 3 || identityIndex(current, testRef("b")) >= 0 {
					t.Fatal("committed unlink lost", current)
				}
				req.ExpectedRevision = 3
				if _, err = control.BindIdentity(ctx, req); err != nil {
					t.Fatal("fresh rebind", err)
				}
			}
		})
	}
}

func TestRealRedisControlledRecovery(t *testing.T) {
	s := realRedisStore(t, true)
	ctx := context.Background()
	a, err := s.CreateAccount(ctx, testRef("a"))
	if err != nil {
		t.Fatal(err)
	}
	actor, err := s.OpenSession(ctx, OpenSessionRequest{Identity: testRef("a"), SessionID: strings.Repeat("a", 32), ExpiresAt: contractTime.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	req := IdentityWrite{Actor: actor.Ref, Reauthenticated: testRef("a"), Target: testRef("b"), ExpectedRevision: 1}
	// The original operation has stopped; no writer runs during this controlled
	// recovery. Routine login/retry paths are not allowed to do this cleanup.
	installRedisFault(t, s, &commandFault{verb: "set", key: s.accountKey(a.AccountID), pipeline: true, err: io.ErrUnexpectedEOF})
	if _, err = s.BindIdentity(ctx, req); !errors.Is(err, ErrResultUnknown) {
		t.Fatal(err)
	}
	current, err := s.GetAccount(ctx, a.AccountID)
	if err != nil || current.IdentityRevision != 1 || identityIndex(current, req.Target) >= 0 {
		t.Fatal("unexpected recovery snapshot", err)
	}
	if owner, err := s.loadLoginAccountID(ctx, req.Target); err != nil || owner != a.AccountID {
		t.Fatal("missing reservation", err)
	}
	if err = s.deleteIndexOnce(ctx, req.Target, a.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BindIdentity(ctx, req); err != nil {
		t.Fatal("fresh authenticated operation failed", err)
	}
	if _, err = s.ResolveAccount(ctx, req.Target); err != nil {
		t.Fatal(err)
	}
}
