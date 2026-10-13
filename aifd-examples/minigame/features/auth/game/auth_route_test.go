package game

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/features/auth"
	"github.com/li9chuan/aifirstdev/aifd-examples/minigame/generated/pbmsg"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	runtimeuser "github.com/li9chuan/aifirstdev/aifd-runtime/user"
)

func TestAuthOKLogsPlayerIn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := rdb.MustNewMemoryDB()
	defer db.Close(context.Background())

	reg := rpc.NewRegistry()
	store, err := runtimeuser.NewStore(runtimeuser.StoreOptions[*pbmsg.DBPlayer]{
		DB: db,
		Key: func(ownerID int64) string {
			return "auth-route-game:" + strconv.FormatInt(ownerID, 10)
		},
		New: func(ownerID int64) *pbmsg.DBPlayer {
			return &pbmsg.DBPlayer{Uid: ownerID}
		},
	})
	if err != nil {
		t.Fatalf("NewStore error = %v", err)
	}
	service := runtimeuser.NewRuntime(reg, store, runtimeuser.NewManager[*pbmsg.DBPlayer]())
	if err := RegisterRPC(reg, service); err != nil {
		t.Fatalf("RegisterRPC error = %v", err)
	}
	bus := rpc.NewLocalBus(rpc.LocalConfig{ServiceName: "auth-route-test", Buffer: 8})
	defer bus.Close(context.Background())
	if err := rpc.Start(ctx, bus, reg, rpc.RunnerOptions{Workers: 1}); err != nil {
		t.Fatalf("Start error = %v", err)
	}

	var rsp pbmsg.MseAuth
	if err := bus.RequestPB(ctx, 1001, auth.TopicGameAuthOK, authReq(5001, 1001, "first", true), &rsp, rpc.RequestOptions{Timeout: time.Second}); err != nil {
		t.Fatalf("RequestPB error = %v", err)
	}
	if rsp.GetRet() != auth.RetOK {
		t.Fatalf("rsp = %+v, want successful new login", &rsp)
	}
	owner, ok := service.GetUser(1001)
	if !ok {
		t.Fatal("owner 1001 is not active")
	}
	dbPlayer := owner.Snapshot()
	if dbPlayer.GetAuth().GetAccountId() != 5001 || dbPlayer.GetAuth().GetTokenId() != "first" || dbPlayer.GetLastSession() != "first" {
		t.Fatalf("dbPlayer = %+v, want auth payload saved", dbPlayer)
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("DB Flush error = %v", err)
	}

	var second pbmsg.MseAuth
	if err := bus.RequestPB(ctx, 1001, auth.TopicGameAuthOK, authReq(5001, 1001, "second", false), &second, rpc.RequestOptions{Timeout: time.Second}); err != nil {
		t.Fatalf("second RequestPB error = %v", err)
	}
	if second.GetRet() != auth.RetOK {
		t.Fatalf("second rsp = %+v, want successful existing login", &second)
	}
	owner, ok = service.GetUser(1001)
	if !ok {
		t.Fatal("owner 1001 is not active after second login")
	}
	if got := owner.Snapshot().GetAuth().GetTokenId(); got != "second" {
		t.Fatalf("auth token id = %q, want second", got)
	}
}

func authReq(accountID int64, uid int64, tokenID string, isNewAccount bool) *pbmsg.MceAuth {
	return &pbmsg.MceAuth{
		Payload: &pbmsg.MAuthPayload{
			AccountId:    accountID,
			Uid:          uid,
			RealmId:      1,
			Provider:     "demo",
			SubjectHash:  strings.Repeat("a", 64),
			TokenId:      tokenID,
			IsNewAccount: isNewAccount,
		},
	}
}
