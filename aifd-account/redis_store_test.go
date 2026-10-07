package account

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-account/generated/accountpb"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

func TestNewRedisStoreRejectsNilClient(t *testing.T) {
	if _, err := NewRedisStore(nil, RedisStoreOptions{}); !errors.Is(err, ErrStoreMissing) {
		t.Fatalf("NewRedisStore error = %v, want ErrStoreMissing", err)
	}
}

func TestRedisStoreLoginCreatesPerKeyAccount(t *testing.T) {
	service, store, server := newRedisTestService(t, nil)
	ctx := context.Background()
	first, err := service.Login(ctx, LoginRequest{
		Credential: demoCredential("alice"),
		AutoCreate: true,
		TokenID:    "first",
	})
	if err != nil {
		t.Fatalf("Login first error = %v", err)
	}
	if first.AccountID != defaultIDStart+1 || !first.IsNewAccount {
		t.Fatalf("first = %+v, want first account", first)
	}
	ref := mustLoginRef(t, Principal{Provider: ProviderDemo, Subject: "alice"})
	loginKey := store.loginKey(ref)
	got, err := server.Get(loginKey)
	if err != nil {
		t.Fatalf("login key missing: %v", err)
	}
	if got != "1001" {
		t.Fatalf("login key value = %q, want 1001", got)
	}
	if server.Exists("account:identity") || server.Exists("account:accounts") || server.Exists("account:owner") {
		t.Fatalf("old global account keys should not exist")
	}
	if strings.Contains(loginKey, "alice") {
		t.Fatalf("login key leaked subject: %s", loginKey)
	}
	second, err := service.Login(ctx, LoginRequest{
		Credential: demoCredential("alice"),
		AutoCreate: true,
		TokenID:    "second",
	})
	if err != nil {
		t.Fatalf("Login second error = %v", err)
	}
	if second.AccountID != first.AccountID || second.IsNewAccount {
		t.Fatalf("second = %+v, want existing account %+v", second, first)
	}
}

func TestRedisStoreLoginWithoutAutoCreateReturnsMissing(t *testing.T) {
	service, _, _ := newRedisTestService(t, nil)
	_, err := service.Login(context.Background(), LoginRequest{Credential: demoCredential("alice")})
	if !errors.Is(err, ErrAccountMissing) {
		t.Fatalf("Login error = %v, want ErrAccountMissing", err)
	}
}

func TestRedisStoreRejectsDisabledAccount(t *testing.T) {
	service, store, _ := newRedisTestService(t, nil)
	result, err := service.Login(context.Background(), LoginRequest{Credential: demoCredential("alice"), AutoCreate: true})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	record := newDBAccount(result.AccountID, 1000)
	record.Status = accountpb.AccountStatus_ACCOUNT_STATUS_DISABLED
	raw, err := proto.Marshal(record)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := store.client.Set(context.Background(), store.accountKey(result.AccountID), raw, 0).Err(); err != nil {
		t.Fatalf("Set account: %v", err)
	}
	_, err = service.Login(context.Background(), LoginRequest{Credential: demoCredential("alice"), AutoCreate: true})
	if !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("Login disabled error = %v, want ErrAccountDisabled", err)
	}
}

func TestRedisStoreSetNXAmbiguousErrorKeepsAccountRecord(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		_ = client.Close()
	})
	wrapped := &setNXAfterWriteErrorClient{
		UniversalClient: client,
		err:             errors.New("ambiguous setnx"),
	}
	store, err := NewRedisStore(wrapped, RedisStoreOptions{})
	if err != nil {
		t.Fatalf("NewRedisStore error = %v", err)
	}
	ref := mustLoginRef(t, Principal{Provider: ProviderDemo, Subject: "alice"})
	wrapped.failKey = store.loginKey(ref)
	service, _ := newTestServiceWithStore(t, store, nil)

	_, err = service.Login(ctx, LoginRequest{Credential: demoCredential("alice"), AutoCreate: true})
	if !errors.Is(err, wrapped.err) {
		t.Fatalf("Login error = %v, want %v", err, wrapped.err)
	}
	loginAccount, err := server.Get(wrapped.failKey)
	if err != nil {
		t.Fatalf("login key missing after ambiguous error: %v", err)
	}
	if loginAccount != "1001" {
		t.Fatalf("login key value = %q, want 1001", loginAccount)
	}
	if !server.Exists(store.accountKey(1001)) {
		t.Fatalf("account record missing after ambiguous error")
	}

	result, err := service.Login(ctx, LoginRequest{Credential: demoCredential("alice"), AutoCreate: true})
	if err != nil {
		t.Fatalf("second Login error = %v", err)
	}
	if result.AccountID != 1001 || result.IsNewAccount {
		t.Fatalf("second result = %+v, want existing account 1001", result)
	}
}

func TestRedisStoreConcurrentSameIdentityLogin(t *testing.T) {
	service, _, _ := newRedisTestService(t, nil)
	ctx := context.Background()
	const workers = 20
	results := make(chan LoginResult, workers)
	errs := make(chan error, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := service.Login(ctx, LoginRequest{Credential: demoCredential("alice"), AutoCreate: true})
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("Login error = %v", err)
	}

	var first LoginResult
	newAccounts := 0
	count := 0
	for result := range results {
		count++
		if count == 1 {
			first = result
		}
		if result.AccountID != first.AccountID {
			t.Fatalf("result = %+v, first = %+v, want one account", result, first)
		}
		if result.IsNewAccount {
			newAccounts++
		}
	}
	if count != workers {
		t.Fatalf("result count = %d, want %d", count, workers)
	}
	if newAccounts != 1 {
		t.Fatalf("new account flags = %d, want one creator", newAccounts)
	}
}

type setNXAfterWriteErrorClient struct {
	redis.UniversalClient
	failKey string
	err     error
	failed  bool
}

func (c *setNXAfterWriteErrorClient) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd {
	cmd := c.UniversalClient.SetNX(ctx, key, value, expiration)
	if key != c.failKey || c.failed || cmd.Err() != nil {
		return cmd
	}
	c.failed = true
	return redis.NewBoolResult(cmd.Val(), c.err)
}

func newRedisTestService(t *testing.T, verifier Verifier, mutate ...func(*Options)) (*Service, *RedisStore, *miniredis.Miniredis) {
	t.Helper()
	store, server := newRedisTestStore(t, RedisStoreOptions{})
	service, _ := newTestServiceWithStore(t, store, verifier, mutate...)
	return service, store, server
}

func newRedisTestStore(t *testing.T, opts RedisStoreOptions) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		_ = client.Close()
	})
	store, err := NewRedisStore(client, opts)
	if err != nil {
		t.Fatalf("NewRedisStore error = %v", err)
	}
	return store, server
}

func mustLoginRef(t *testing.T, principal Principal) LoginRef {
	t.Helper()
	ref, err := LoginRefFromPrincipal(principal)
	if err != nil {
		t.Fatalf("LoginRefFromPrincipal: %v", err)
	}
	return ref
}
