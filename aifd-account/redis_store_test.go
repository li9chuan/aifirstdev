package account

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newRedisTestStore(t *testing.T) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store, err := NewRedisStore(client, RedisStoreOptions{Space: "test", Now: func() time.Time { return contractTime }})
	if err != nil {
		t.Fatal(err)
	}
	return store, server
}

func TestRedisStoreContract(t *testing.T) {
	runStoreContract(t, func(t *testing.T) Store { s, _ := newRedisTestStore(t); return s })
}

func TestRedisKeysAndStoredShape(t *testing.T) {
	s, server := newRedisTestStore(t)
	a, err := s.CreateAccount(context.Background(), testRef("private-subject"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := server.Get(s.loginKey(testRef("private-subject")))
	if err != nil || value != formatAccountID(a.AccountID) {
		t.Fatalf("index=%s %v", value, err)
	}
	for _, key := range server.Keys() {
		if strings.ContainsAny(key, "{}") || strings.Contains(key, "private-subject") {
			t.Fatal("invalid key", key)
		}
	}
	raw, err := server.Get(s.accountKey(a.AccountID))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := unmarshalAccount([]byte(raw), a.AccountID)
	if err != nil || decoded.IdentityRevision != 1 || decoded.SessionEpoch != 1 {
		t.Fatalf("record=%+v %v", decoded, err)
	}
	if server.TTL(s.accountKey(a.AccountID)) != 0 || server.TTL(s.loginKey(testRef("private-subject"))) != 0 {
		t.Fatal("account/index TTL set")
	}
}

func TestRedisPendingIdentityDoesNotBlockOtherSessions(t *testing.T) {
	s, _ := newRedisTestStore(t)
	ctx := context.Background()
	a, err := s.CreateAccount(ctx, testRef("a"))
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.OpenSession(ctx, OpenSessionRequest{Identity: testRef("a"), SessionID: strings.Repeat("a", 32), ExpiresAt: contractTime.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.client.Set(ctx, s.loginKey(testRef("pending")), formatAccountID(a.AccountID), 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveAccount(ctx, testRef("pending")); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err = s.BindIdentity(ctx, IdentityWrite{Actor: session.Ref, Reauthenticated: testRef("a"), Target: testRef("pending"), ExpectedRevision: 1}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("adopted pending reservation", err)
	}
	if _, err = s.ValidateSession(ctx, session.Ref); err != nil {
		t.Fatal("unrelated pending identity invalidated session", err)
	}
}

func TestRedisCorruptionIsNotMissingAccount(t *testing.T) {
	s, _ := newRedisTestStore(t)
	ctx := context.Background()
	a, err := s.CreateAccount(ctx, testRef("a"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.client.Set(ctx, s.accountKey(a.AccountID), []byte{0xff}, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveAccount(ctx, testRef("a")); !errors.Is(err, ErrCorruptState) {
		t.Fatal(err)
	}
	if err = s.client.Set(ctx, s.loginKey(testRef("a")), "01", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveAccount(ctx, testRef("a")); !errors.Is(err, ErrCorruptState) {
		t.Fatal(err)
	}
}

func TestRedisConfigurationRejectsUnsafeNamespaces(t *testing.T) {
	if _, err := NewRedisStore(nil, RedisStoreOptions{Space: "test"}); !errors.Is(err, ErrStoreMissing) {
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{})
	defer client.Close()
	for _, space := range []string{"", "{shared}", "a:b", "a/b"} {
		if _, err := NewRedisStore(client, RedisStoreOptions{Space: space}); err == nil {
			t.Fatal("accepted space", space)
		}
	}
	cluster := redis.NewClusterClient(&redis.ClusterOptions{Addrs: []string{"127.0.0.1:1"}, ReadOnly: true})
	defer cluster.Close()
	if _, err := NewRedisStore(cluster, RedisStoreOptions{Space: "test"}); err == nil {
		t.Fatal("accepted replica reads")
	}
}
