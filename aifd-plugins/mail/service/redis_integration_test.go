package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	mail "github.com/li9chuan/aifirstdev/aifd-plugins/mail"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/internal/redistest"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	redis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

func TestRealRedisHashRecoveryCDAndDelete(t *testing.T) {
	server := redistest.Start(t)
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: server.Addr, MaxRetries: -1})
	defer client.Close()
	prefix := "mail:integration"
	legacy := &mailpb.MailData{MailId: 1, Type: mailpb.MailType_MAIL_TYPE_PERSONAL, Base: &mailpb.MailBase{TemplateId: 1}, Read: true, EndTimeUnix: time.Now().Add(time.Hour).Unix()}
	raw, _ := proto.Marshal(legacy)
	key := prefix + ":user:7:data"
	if err := client.HSet(ctx, key, "1", raw).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Expire(ctx, key, time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	gen := &seqGenerator{}
	gen.next.Store(100)
	db, err := rdb.NewRedisDB(client, rdb.WithSaveDelay(time.Hour, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := NewModule(Options{DB: db, Prefix: prefix, IDGenerator: gen, Awarder: mail.AwarderFunc(func(context.Context, int64, *mailpb.MailData) error {
		return fmt.Errorf("injected ordinary award failure")
	})})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(ctx)
	defer db.Close(ctx)
	if err := owner.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	data := owner.Get(ctx, 7, &mailpb.MailGetRequest{MailId: 1}).GetMail()
	if !data.GetRead() {
		t.Fatal("legacy read flag lost")
	}
	if ttl := client.TTL(ctx, key).Val(); ttl != -1 {
		t.Fatalf("old key TTL not cleared: %v", ttl)
	}
	long := personal(7, true)
	long.SaveDays = 10
	longID := send(t, owner, long)
	cooldown := owner.Claim(ctx, 7, &mailpb.MailIDsRequest{MailIdList: []int64{longID}})
	if cooldown.GetRet() != mail.RetInternal || cooldown.GetRetryAtUnixMs() == 0 {
		t.Fatal(cooldown.String())
	}
	batch := owner.AddBatch(ctx, &mailpb.MailAddBatchRequest{MailList: []*mailpb.MailAddRequest{shared("L0"), shared("server:1")}})
	if batch.GetRet() != mail.RetOK || len(batch.GetMailIdList()) != 2 {
		t.Fatal(batch.String())
	}
	sharedID, serverID := batch.MailIdList[0], batch.MailIdList[1]
	a := []string{"L0", "server:1"}
	if len(list(t, owner, 7, a)) != 4 {
		t.Fatal("overlapping member missed one independently addressed mail")
	}
	owner.Read(ctx, 7, &mailpb.MailIDsRequest{GroupKeys: a, MailIdList: []int64{sharedID}})
	deleted := owner.DeleteRead(ctx, 7, &mailpb.MailIDsRequest{GroupKeys: a, MailIdList: []int64{sharedID}})
	if len(deleted.GetMailIdList()) != 1 {
		t.Fatal(deleted.String())
	}
	if err := owner.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if exists := client.HExists(ctx, key, fmt.Sprint(sharedID)).Val(); exists {
		t.Fatal("delete did not HDEL")
	}
	values, err := client.HGetAll(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	stored := &mailpb.MailData{}
	if err := proto.Unmarshal([]byte(values[fmt.Sprint(longID)]), stored); err != nil {
		t.Fatal(err)
	}
	if stored.GetClaimed() || stored.GetClaimRetryAtUnixMs() != cooldown.GetRetryAtUnixMs() {
		t.Fatal("CD not saved as MailData")
	}
	sharedRaw, err := client.HGet(ctx, prefix+":shared:data", fmt.Sprint(serverID)).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	storedSource := &mailpb.MailData{}
	if err := proto.Unmarshal(sharedRaw, storedSource); err != nil || storedSource.GetGroupKey() != "server:1" || len(storedSource.GetGroupKeys()) != 0 || len(storedSource.GetTargetRealms()) != 0 {
		t.Fatal("shared source did not persist a single opaque group", storedSource, err)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatal(err)
	}
	server.Restart(t)
	client.Close()
	client = redis.NewClient(&redis.Options{Addr: server.Addr, MaxRetries: -1})
	defer client.Close()
	restoredDB, err := rdb.NewRedisDB(client)
	if err != nil {
		t.Fatal(err)
	}
	defer restoredDB.Close(ctx)
	restored, err := NewModule(Options{DB: restoredDB, Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close(ctx)
	if err := restored.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	for _, groups := range [][]string{{"L1", "server:1"}, {"L0", "server:1"}} {
		mails := list(t, restored, 7, groups)
		if len(mails) != 3 {
			t.Fatal("independent mail missing after restart", mails)
		}
		foundServer := false
		for _, data := range mails {
			if data.GetMailId() == sharedID {
				t.Fatal("deleted shared mail resurrected on rejoining after Redis/owner restart")
			}
			foundServer = foundServer || data.GetMailId() == serverID
		}
		if !foundServer {
			t.Fatal("independent server group mail lost")
		}
	}
	metaRaw, err := client.HGet(ctx, prefix+":user:7:meta", "meta").Bytes()
	if err != nil {
		t.Fatal(err)
	}
	meta := &mailpb.MailboxMeta{}
	if err := proto.Unmarshal(metaRaw, meta); err != nil || len(meta.GetGroupCursors()) != 2 {
		t.Fatal("per-group cursors did not survive restart", meta, err)
	}
	result := restored.Get(ctx, 7, &mailpb.MailGetRequest{MailId: longID})
	if result.GetRet() != mail.RetOK || result.GetMail().GetClaimRetryAtUnixMs() != cooldown.GetRetryAtUnixMs() {
		t.Fatal(result.String())
	}
	if result.GetMail().GetEndTimeUnix() <= legacy.GetEndTimeUnix() {
		t.Fatal("short old key lifetime clipped long mail")
	}
	keys, err := client.Keys(ctx, prefix+":*").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if strings.HasSuffix(key, ":index") {
			t.Fatal("new owner wrote legacy ZSET")
		}
	}
}
