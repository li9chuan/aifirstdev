package app

import (
	"context"
	"encoding/hex"
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

	store, err := account.NewRedisStore(client, account.RedisStoreOptions{Space: "minigame"})
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
	if accountID <= 0 || first.GetPayload().GetUid() != accountID || first.GetPayload().GetSessionId() == "" || first.GetToken() == "" {
		t.Fatalf("first auth = %+v, want new Redis-backed account auth", first)
	}

	snapshot, err := app.Service.GetAccount(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Identities) != 1 {
		t.Fatal("identity set", snapshot)
	}
	ref := snapshot.Identities[0]
	loginAccount, err := client.Get(ctx, "account:minigame:login:"+ref.AuthorityID+":"+hex.EncodeToString(ref.SubjectDigest[:])).Result()
	if err != nil {
		t.Fatalf("GET login key error = %v", err)
	}
	if loginAccount != strconv.FormatInt(accountID, 10) {
		t.Fatalf("login account = %q, want %d", loginAccount, accountID)
	}
	if !server.Exists("account:minigame:act:" + strconv.FormatInt(accountID, 10)) {
		t.Fatalf("account record key missing for %d", accountID)
	}

	secondStore, err := account.NewRedisStore(client, account.RedisStoreOptions{Space: "minigame"})
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
	if second.GetPayload().GetAccountId() != accountID || second.GetPayload().GetUid() != accountID || second.GetPayload().GetSessionId() == first.GetPayload().GetSessionId() {
		t.Fatalf("second auth = %+v, want persisted existing account", second)
	}
}
