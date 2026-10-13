package app

import (
	"context"
	"strconv"
	"testing"

	"github.com/alicebob/miniredis/v2"
	account "github.com/li9chuan/aifirstdev/aifd-account"
	"github.com/redis/go-redis/v9"
)

func TestAccountAppCanUseRedisStore(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		_ = client.Close()
	})

	store, err := account.NewRedisStore(client, account.RedisStoreOptions{})
	if err != nil {
		t.Fatalf("NewRedisStore error = %v", err)
	}
	app, err := NewAccountAppWithStore(store)
	if err != nil {
		t.Fatalf("NewAccountAppWithStore error = %v", err)
	}
	first, err := app.DemoAuth(ctx, 4001)
	if err != nil {
		t.Fatalf("DemoAuth first error = %v", err)
	}
	accountID := first.GetPayload().GetAccountId()
	if accountID <= 0 || first.GetPayload().GetUid() != accountID || !first.GetPayload().GetIsNewAccount() || first.GetToken() == "" {
		t.Fatalf("first auth = %+v, want new Redis-backed account auth", first)
	}

	ref, err := account.LoginRefFromPrincipal(account.Principal{Provider: account.ProviderDemo, Subject: "4001"})
	if err != nil {
		t.Fatalf("LoginRefFromPrincipal error = %v", err)
	}
	loginAccount, err := client.Get(ctx, "account:login:"+ref.Provider+":"+ref.SubjectHash).Result()
	if err != nil {
		t.Fatalf("GET login key error = %v", err)
	}
	if loginAccount != strconv.FormatInt(accountID, 10) {
		t.Fatalf("login account = %q, want %d", loginAccount, accountID)
	}
	if !server.Exists("account:act:" + strconv.FormatInt(accountID, 10)) {
		t.Fatalf("account record key missing for %d", accountID)
	}

	secondStore, err := account.NewRedisStore(client, account.RedisStoreOptions{})
	if err != nil {
		t.Fatalf("NewRedisStore second error = %v", err)
	}
	secondApp, err := NewAccountAppWithStore(secondStore)
	if err != nil {
		t.Fatalf("NewAccountAppWithStore second error = %v", err)
	}
	second, err := secondApp.DemoAuth(ctx, 4001)
	if err != nil {
		t.Fatalf("DemoAuth second error = %v", err)
	}
	if second.GetPayload().GetAccountId() != accountID || second.GetPayload().GetUid() != accountID || second.GetPayload().GetIsNewAccount() {
		t.Fatalf("second auth = %+v, want persisted existing account", second)
	}
}
