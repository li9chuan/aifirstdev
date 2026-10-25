package mailbox

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mail "github.com/li9chuan/aifirstdev/aifd-plugins/mail"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/internal/redistest"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	redis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

type expiryWindowHook struct {
	key  string
	once sync.Once
}

func (h *expiryWindowHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *expiryWindowHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *expiryWindowHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if err == nil && cmd.Name() == "hgetall" && len(cmd.Args()) > 1 && fmt.Sprint(cmd.Args()[1]) == h.key {
			h.once.Do(func() { time.Sleep(500 * time.Millisecond) })
		}
		return err
	}
}

func TestRealRedisTTLIsRemovedBeforePayloadLoad(t *testing.T) {
	server := redistest.Start(t)
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: server.Addr, MaxRetries: -1})
	defer client.Close()
	key := "aifd:mail:user:7:data"
	data := &mailpb.MailData{MailId: 11, Type: mailpb.MailType_MAIL_TYPE_PERSONAL, Base: &mailpb.MailBase{TemplateId: 1}, Read: true, EndTimeUnix: time.Now().Add(time.Hour).Unix()}
	raw, err := proto.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, key, "11", raw).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.PExpire(ctx, key, 250*time.Millisecond).Err(); err != nil {
		t.Fatal(err)
	}
	client.AddHook(&expiryWindowHook{key: key})
	db, err := rdb.NewRedisDB(client, rdb.WithSaveDelay(time.Hour, time.Hour), rdb.WithFailureFileDir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	m, err := NewManager(Options{DB: db, IDGenerator: &sequence{}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(ctx)
	if err := m.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if got, found, err := m.Get(ctx, 7, nil, 11); err != nil || !found || !got.GetRead() {
		t.Fatal(got, found, err)
	}
	if !client.HExists(ctx, key, "11").Val() || client.TTL(ctx, key).Val() != -1 {
		t.Fatal("key expired in recovery window")
	}
	if err := m.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatal(err)
	}
	server.Restart(t)
	client.Close()
	restoredClient := redis.NewClient(&redis.Options{Addr: server.Addr, MaxRetries: -1})
	defer restoredClient.Close()
	restoredDB, err := rdb.NewRedisDB(restoredClient)
	if err != nil {
		t.Fatal(err)
	}
	defer restoredDB.Close(ctx)
	restored := newTestManager(t, Options{DB: restoredDB})
	if got, found, err := restored.Get(ctx, 7, nil, 11); err != nil || !found || !got.GetRead() {
		t.Fatal("normal Redis/owner restart lost recovered mail", got, found, err)
	}
}

func TestRealRedisFailedClaimSaveBlocksEvictionUntilWritten(t *testing.T) {
	server := redistest.Start(t)
	ctx := context.Background()
	admin := redis.NewClient(&redis.Options{Addr: server.Addr, MaxRetries: -1})
	defer admin.Close()
	if err := admin.Do(ctx, "ACL", "SETUSER", "mail-test", "on", ">test-only", "~*", "+@all").Err(); err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{Addr: server.Addr, Username: "mail-test", Password: "test-only", MaxRetries: -1})
	defer client.Close()
	db, err := rdb.NewRedisDB(client, rdb.WithSaveDelay(time.Hour, time.Hour), rdb.WithFailureFileDir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	var awards atomic.Int64
	m := newTestManager(t, Options{DB: db, CacheCapacity: 1, Awarder: mail.AwarderFunc(func(context.Context, int64, *mailpb.MailData) error { awards.Add(1); return nil })})
	req := testRequest()
	req.Base.ItemList = []*mailpb.MailItem{{Tid: 1, Num: 1}}
	data, err := m.AddPersonal(ctx, 7, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := admin.Do(ctx, "ACL", "SETUSER", "mail-test", "-hset").Err(); err != nil {
		t.Fatal(err)
	}
	if result, err := m.Claim(ctx, 7, nil, []int64{data.GetMailId()}); err != nil || len(result.MailIDs) != 1 || awards.Load() != 1 {
		t.Fatal(result, err)
	}
	var retryAt int64
	for range 2 {
		if _, err := m.AddPersonal(ctx, 8, testRequest()); err == nil {
			t.Fatal("unresolved HSET failure permitted cache eviction")
		}
		got, found, err := m.Get(ctx, 7, nil, data.GetMailId())
		if err != nil || !found || !got.GetClaimed() || got.GetClaimRetryAtUnixMs() == 0 {
			t.Fatal("failed eviction lost canonical claim state", got, err)
		}
		retryAt = got.GetClaimRetryAtUnixMs()
	}
	if err := admin.Do(ctx, "ACL", "SETUSER", "mail-test", "+hset").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddPersonal(ctx, 8, testRequest()); err != nil {
		t.Fatal("repaired write did not permit eviction", err)
	}
	got, found, err := m.Get(ctx, 7, nil, data.GetMailId())
	if err != nil || !found || !got.GetClaimed() || !got.GetRead() || got.GetClaimRetryAtUnixMs() != retryAt {
		t.Fatal("reloaded stale claim state", got, err)
	}
	if result, err := m.Claim(ctx, 7, nil, []int64{data.GetMailId()}); err != nil || len(result.MailIDs) != 0 || awards.Load() != 1 {
		t.Fatal("completed award repeated after recovery", result, err)
	}
	// Close retries must obey the same persistence barrier as eviction.
	plain, err := m.AddPersonal(ctx, 7, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := admin.Do(ctx, "ACL", "SETUSER", "mail-test", "-hset").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.MarkRead(ctx, 7, nil, []int64{plain.GetMailId()}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := m.Close(ctx); err == nil {
			t.Fatal("Close forgot an unresolved write")
		}
	}
	if err := admin.Do(ctx, "ACL", "SETUSER", "mail-test", "+hset").Err(); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(ctx); err != nil {
		t.Fatal(err)
	}
	restored := newTestManager(t, Options{DB: db})
	if got, found, err := restored.Get(ctx, 7, nil, plain.GetMailId()); err != nil || !found || !got.GetRead() {
		t.Fatal("repaired Close lost read state", got, err)
	}
}

func TestRealRedisFailedSharedDeleteSurvivesEvictionAndRecovery(t *testing.T) {
	server := redistest.Start(t)
	ctx := context.Background()
	admin := redis.NewClient(&redis.Options{Addr: server.Addr, MaxRetries: -1})
	defer admin.Close()
	if err := admin.Do(ctx, "ACL", "SETUSER", "mail-test", "on", ">test-only", "~*", "+@all").Err(); err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{Addr: server.Addr, Username: "mail-test", Password: "test-only", MaxRetries: -1})
	defer client.Close()
	db, err := rdb.NewRedisDB(client, rdb.WithSaveDelay(time.Hour, time.Hour), rdb.WithFailureFileDir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	m := newTestManager(t, Options{DB: db, CacheCapacity: 1})
	data, err := m.PublishShared(ctx, &mailpb.MailAddRequest{Type: mailpb.MailType_MAIL_TYPE_GROUP, GroupKey: "A", Base: &mailpb.MailBase{TemplateId: 1}})
	if err != nil {
		t.Fatal(err)
	}
	groups := []string{"A"}
	ids := []int64{data.GetMailId()}
	if err := m.SyncShared(ctx, 7, groups); err != nil {
		t.Fatal(err)
	}
	if _, err := m.MarkRead(ctx, 7, groups, ids); err != nil {
		t.Fatal(err)
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := admin.Do(ctx, "ACL", "SETUSER", "mail-test", "-hdel").Err(); err != nil {
		t.Fatal(err)
	}
	if deleted, err := m.DeleteRead(ctx, 7, groups, ids); err != nil || len(deleted) != 1 {
		t.Fatal(deleted, err)
	}
	for range 2 {
		if _, err := m.AddPersonal(ctx, 8, testRequest()); err == nil {
			t.Fatal("unresolved HDEL failure permitted eviction")
		}
	}
	if err := admin.Do(ctx, "ACL", "SETUSER", "mail-test", "+hdel").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddPersonal(ctx, 8, testRequest()); err != nil {
		t.Fatal(err)
	}
	if got, err := m.List(ctx, 7, groups, 0, 100); err != nil || len(got) != 0 {
		t.Fatal("failed delete resurrected after repair and eviction", got, err)
	}
	if err := m.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if admin.HExists(ctx, "aifd:mail:user:7:data", fmt.Sprint(data.GetMailId())).Val() {
		t.Fatal("repaired HDEL not persisted")
	}
	restored := newTestManager(t, Options{DB: db})
	if got, err := restored.List(ctx, 7, groups, 0, 100); err != nil || len(got) != 0 {
		t.Fatal("cursor failed to preserve deletion after restart", got, err)
	}
}
