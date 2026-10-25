package mailbox

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	redis "github.com/redis/go-redis/v9"
)

type sequence struct{ n atomic.Int64 }

func (s *sequence) NextID() (int64, error) { return s.n.Add(1), nil }
func newTestManager(t *testing.T, opts Options) *Manager {
	t.Helper()
	if opts.DB == nil {
		opts.DB = rdb.MustNewMemoryDB(rdb.WithSaveDelay(time.Hour, time.Hour))
		t.Cleanup(func() { opts.DB.Close(context.Background()) })
	}
	if opts.IDGenerator == nil {
		opts.IDGenerator = &sequence{}
	}
	m, err := NewManager(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return m
}
func testRequest() *mailpb.MailAddRequest {
	return &mailpb.MailAddRequest{Type: mailpb.MailType_MAIL_TYPE_PERSONAL, Base: &mailpb.MailBase{TemplateId: 1}}
}

func TestCanonicalConcurrentLoadAndMutations(t *testing.T) {
	m := newTestManager(t, Options{MailboxLimit: 200})
	var first atomic.Pointer[mailboxEntry]
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			box, release, err := m.acquire(context.Background(), 7)
			if err != nil {
				t.Error(err)
				return
			}
			defer release()
			first.CompareAndSwap(nil, box)
			if first.Load() != box {
				t.Error("duplicate canonical object")
			}
			if _, err := m.AddPersonal(context.Background(), 7, testRequest()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got, err := m.List(context.Background(), 7, nil, 0, 200)
	if err != nil || len(got) != 100 {
		t.Fatalf("got %d, err %v", len(got), err)
	}
	got[0].Base.TemplateId = 999
	again, _, err := m.Get(context.Background(), 7, nil, got[0].GetMailId())
	if err != nil || again.GetBase().GetTemplateId() != 1 {
		t.Fatal("mutable state escaped")
	}
}

func TestEvictionFlushesBeforeReloadAndRetainsClaimCD(t *testing.T) {
	m := newTestManager(t, Options{CacheCapacity: 1})
	data, err := m.AddPersonal(context.Background(), 7, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.MarkRead(context.Background(), 7, nil, []int64{data.GetMailId()}); err != nil {
		t.Fatal(err)
	}
	if _, err = m.AddPersonal(context.Background(), 8, testRequest()); err != nil {
		t.Fatal(err)
	}
	restored, found, err := m.Get(context.Background(), 7, nil, data.GetMailId())
	if err != nil || !found || !restored.GetRead() {
		t.Fatalf("eviction reloaded stale state: %v %v %v", restored, found, err)
	}
}

func TestFailedRedisFlushKeepsCanonicalDirtyMailbox(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1, DialTimeout: 30 * time.Millisecond, ReadTimeout: 30 * time.Millisecond, WriteTimeout: 30 * time.Millisecond})
	defer client.Close()
	db, err := rdb.NewRedisDB(client, rdb.WithSaveDelay(time.Hour, time.Hour), rdb.WithFailureFileDir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.Background())
	m := newTestManager(t, Options{DB: db, CacheCapacity: 1})
	data, err := m.AddPersonal(context.Background(), 7, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	box, release, err := m.acquire(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	release()
	server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	_, err = m.AddPersonal(ctx, 8, testRequest())
	cancel()
	if err == nil {
		t.Fatal("dirty eviction unexpectedly succeeded")
	}
	got, found, err := m.Get(context.Background(), 7, nil, data.GetMailId())
	if err != nil || !found || got.GetMailId() != data.GetMailId() {
		t.Fatal("failed flush lost canonical state")
	}
	again, release, err := m.acquire(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if again != box {
		t.Fatal("failed flush replaced canonical object")
	}
	if err := server.Restart(); err != nil {
		t.Fatal(err)
	}
	if err := db.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRejectedSaveDoesNotPublishReadOrNewMail(t *testing.T) {
	db := rdb.MustNewMemoryDB(rdb.WithSaveDelay(time.Hour, time.Hour))
	m := newTestManager(t, Options{DB: db})
	data, err := m.AddPersonal(context.Background(), 7, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.MarkRead(context.Background(), 7, nil, []int64{data.GetMailId()}); err == nil {
		t.Fatal("closed save queue accepted read")
	}
	got, _, _ := m.Get(context.Background(), 7, nil, data.GetMailId())
	if got.GetRead() {
		t.Fatal("rejected read leaked into memory")
	}
	if _, err := m.AddPersonal(context.Background(), 7, testRequest()); err == nil {
		t.Fatal("closed save queue accepted mail")
	}
	mails, err := m.List(context.Background(), 7, nil, 0, 100)
	if err != nil || len(mails) != 1 {
		t.Fatal("rejected add published")
	}
}

func TestExpiredSourceCleanupDoesNotDeliverNewSharedMail(t *testing.T) {
	m := newTestManager(t, Options{})
	_, _, err := m.Get(context.Background(), 7, nil, 999)
	if err != nil {
		t.Fatal(err)
	}
	data, err := m.PublishShared(context.Background(), &mailpb.MailAddRequest{Type: mailpb.MailType_MAIL_TYPE_GLOBAL, Base: &mailpb.MailBase{TemplateId: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.CleanupExpired(context.Background(), time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := m.Get(context.Background(), 7, nil, data.GetMailId()); found {
		t.Fatal("cleanup materialized mail without an audience request")
	}
}

func TestLegacyBodiesAndCursorFloorRecover(t *testing.T) {
	db := rdb.MustNewMemoryDB(rdb.WithSaveDelay(time.Hour, time.Hour))
	defer db.Close(context.Background())
	source := &mailpb.MailData{MailId: 11, Type: mailpb.MailType_MAIL_TYPE_GROUP, Base: &mailpb.MailBase{TemplateId: 1}, GroupKeys: []string{"A"}, EndTimeUnix: time.Now().Add(time.Hour).Unix()}
	saver := &rdb.HashSaver[*mailpb.MailData]{}
	saver.BindDB(db, "aifd:mail:shared:data", "11", source)
	saver.Save(context.Background())
	meta := &rdb.HashSaver[*mailpb.MailboxMeta]{}
	meta.BindDB(db, "aifd:mail:user:7:meta", "meta", &mailpb.MailboxMeta{GroupCursors: []*mailpb.MailGroupCursor{{GroupKey: "A", LastMailId: 11}}})
	meta.Save(context.Background())
	oldCopy := &mailpb.MailData{MailId: 11, Type: mailpb.MailType_MAIL_TYPE_PERSONAL, Base: source.Base, Read: true, Claimed: true, EndTimeUnix: source.EndTimeUnix}
	body := &rdb.HashSaver[*mailpb.MailData]{}
	body.BindDB(db, "aifd:mail:user:8:data", "11", oldCopy)
	body.Save(context.Background())
	if err := db.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	m := newTestManager(t, Options{DB: db})
	a := []string{"A", "B"}
	got, err := m.List(context.Background(), 7, a, 0, 100)
	if err != nil || len(got) != 0 {
		t.Fatal("legacy deleted mail reissued")
	}
	if hidden, err := m.List(context.Background(), 8, []string{"B"}, 0, 100); err != nil || len(hidden) != 0 {
		t.Fatal("first load of legacy PERSONAL copy bypassed group eligibility", hidden, err)
	}
	got, err = m.List(context.Background(), 8, a, 0, 100)
	if err != nil || len(got) != 1 || !got[0].GetRead() || !got[0].GetClaimed() {
		t.Fatal("legacy copy state reset")
	}
	if _, found, err := m.Get(context.Background(), 8, []string{"B"}, 11); err != nil || found {
		t.Fatal("legacy PERSONAL copy bypassed group membership")
	}
	if got, err := m.List(context.Background(), 8, []string{"B"}, 0, 100); err != nil || len(got) != 0 {
		t.Fatal("restricted group copy escaped through a narrow list snapshot", got, err)
	}
}

func TestStoredCorruptionFailsInsteadOfCreatingEmptyMailbox(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	db, err := rdb.NewRedisDB(client)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.Background())
	server.HSet("aifd:mail:user:7:data", "1", "not protobuf")
	m := newTestManager(t, Options{DB: db})
	if _, _, err := m.Get(context.Background(), 7, nil, 1); err == nil {
		t.Fatal("corrupt field treated as empty")
	}
	if got := server.HGet("aifd:mail:user:7:data", "1"); got != "not protobuf" {
		t.Fatal("corrupt bytes overwritten")
	}
}

func TestSingleOwnerRejectsRegressingSharedID(t *testing.T) {
	db := rdb.MustNewMemoryDB(rdb.WithSaveDelay(time.Hour, time.Hour))
	defer db.Close(context.Background())
	m := newTestManager(t, Options{DB: db})
	req := &mailpb.MailAddRequest{Type: mailpb.MailType_MAIL_TYPE_GLOBAL, Base: &mailpb.MailBase{TemplateId: 1}}
	if _, err := m.PublishShared(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored := newTestManager(t, Options{DB: db, IDGenerator: &sequence{}})
	if _, err := restored.PublishShared(context.Background(), req); err == nil {
		t.Fatal("regressing ID published")
	}
	if got, err := restored.List(context.Background(), 7, nil, 0, 100); err != nil || len(got) != 1 {
		t.Fatal(fmt.Sprint(got), err)
	}
}

func TestOldSharedSnapshotCannotResetReadOrResurrectDelete(t *testing.T) {
	m := newTestManager(t, Options{})
	ctx := context.Background()
	data, err := m.PublishShared(ctx, &mailpb.MailAddRequest{Type: mailpb.MailType_MAIL_TYPE_GROUP, Base: &mailpb.MailBase{TemplateId: 1}, GroupKey: "A"})
	if err != nil {
		t.Fatal(err)
	}
	a := []string{"A"}
	b := []string{"A", "B"}
	if err := m.SyncShared(ctx, 7, a); err != nil {
		t.Fatal(err)
	}
	oldSnapshot := m.snapshotShared(a)
	box, release, err := m.acquire(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if err := m.syncShared(ctx, 7, box, b, oldSnapshot); err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			if _, err := m.MarkRead(ctx, 7, a, []int64{data.GetMailId()}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if got, _, _ := m.Get(ctx, 7, b, data.GetMailId()); !got.GetRead() {
		t.Fatal("stale synchronization reset read state")
	}
	if ids, err := m.DeleteRead(ctx, 7, b, []int64{data.GetMailId()}); err != nil || len(ids) != 1 {
		t.Fatal(ids, err)
	}
	for range 20 {
		wg.Go(func() {
			if err := m.syncShared(ctx, 7, box, a, oldSnapshot); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if _, found, _ := m.Get(ctx, 7, a, data.GetMailId()); found {
		t.Fatal("stale synchronization resurrected deletion")
	}
}

func TestSnapshotBeforeGroupPublicationCannotClearProgress(t *testing.T) {
	m := newTestManager(t, Options{})
	ctx := context.Background()
	oldSnapshot := m.snapshotShared([]string{"A"})
	data, err := m.PublishShared(ctx, &mailpb.MailAddRequest{Type: mailpb.MailType_MAIL_TYPE_GROUP, Base: &mailpb.MailBase{TemplateId: 1}, GroupKey: "A"})
	if err != nil {
		t.Fatal(err)
	}
	groups := []string{"A"}
	if err := m.SyncShared(ctx, 7, groups); err != nil {
		t.Fatal(err)
	}
	if _, err := m.MarkRead(ctx, 7, groups, []int64{data.GetMailId()}); err != nil {
		t.Fatal(err)
	}
	if ids, err := m.DeleteRead(ctx, 7, groups, []int64{data.GetMailId()}); err != nil || len(ids) != 1 {
		t.Fatal(ids, err)
	}
	box, release, err := m.acquire(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, memberships := range [][]string{nil, {"A"}, {"B"}} {
		if err := m.syncShared(ctx, 7, box, memberships, oldSnapshot); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := m.List(ctx, 7, groups, 0, 100); err != nil || len(got) != 0 {
		t.Fatal("old empty snapshot cleared newer group progress", got, err)
	}
}

func TestLegacySharedTargetRecoveryFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name      string
		typ       mailpb.MailType
		groups    []string
		realms    []int32
		expired   bool
		wantError bool
	}{
		{"single-group", mailpb.MailType_MAIL_TYPE_GROUP, []string{"A", "A"}, nil, false, false},
		{"multi-group", mailpb.MailType_MAIL_TYPE_GROUP, []string{"A", "B"}, nil, false, true},
		{"group-realm", mailpb.MailType_MAIL_TYPE_GROUP, []string{"A"}, []int32{1}, false, true},
		{"global-realm", mailpb.MailType_MAIL_TYPE_GLOBAL, nil, []int32{1}, false, true},
		{"invalid-group", mailpb.MailType_MAIL_TYPE_GROUP, []string{"", "A"}, nil, false, true},
		{"expired-combination", mailpb.MailType_MAIL_TYPE_GROUP, []string{"A", "B"}, []int32{1}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := rdb.MustNewMemoryDB(rdb.WithSaveDelay(time.Hour, time.Hour))
			defer db.Close(ctx)
			end := time.Now().Add(time.Hour).Unix()
			if tc.expired {
				end = time.Now().Add(-time.Hour).Unix()
			}
			source := &mailpb.MailData{MailId: 11, Type: tc.typ, Base: &mailpb.MailBase{TemplateId: 1}, GroupKeys: tc.groups, TargetRealms: tc.realms, EndTimeUnix: end}
			saver := &rdb.HashSaver[*mailpb.MailData]{}
			if err := saver.BindDB(db, "aifd:mail:shared:data", "11", source); err != nil {
				t.Fatal(err)
			}
			if err := saver.Save(ctx); err != nil {
				t.Fatal(err)
			}
			if err := db.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			m, err := NewManager(Options{DB: db, IDGenerator: &sequence{}})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close(ctx)
			if err := m.Recover(ctx); (err != nil) != tc.wantError {
				t.Fatalf("recovery error = %v, wantError %v", err, tc.wantError)
			}
			if tc.wantError {
				return
			}
			mails, err := m.List(ctx, 7, []string{"A"}, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			if tc.expired {
				if len(mails) != 0 {
					t.Fatal("expired unsupported target delivered")
				}
				if err := m.CleanupExpired(ctx, time.Now().Unix()); err != nil {
					t.Fatal(err)
				}
				if err := db.Flush(ctx); err != nil {
					t.Fatal(err)
				}
				stored, err := rdb.LoadHash(ctx, db, "aifd:mail:shared:data", func() *mailpb.MailData { return &mailpb.MailData{} })
				if err != nil || len(stored) != 0 {
					t.Fatal("expired unsupported source not cleaned", stored, err)
				}
			} else if len(mails) != 1 || mails[0].GetGroupKey() != "A" || len(mails[0].GetGroupKeys()) != 0 {
				t.Fatal("legacy single group was not normalized", mails)
			}
		})
	}
}

func TestUnreleasedCombinationMetadataIsNotSilentlyReset(t *testing.T) {
	ctx := context.Background()
	db := rdb.MustNewMemoryDB(rdb.WithSaveDelay(time.Hour, time.Hour))
	defer db.Close(ctx)
	meta := &mailpb.MailboxMeta{}
	// Removed draft field 3, an empty MailSharedCursor payload.
	meta.ProtoReflect().SetUnknown([]byte{0x1a, 0x00})
	saver := &rdb.HashSaver[*mailpb.MailboxMeta]{}
	if err := saver.BindDB(db, "aifd:mail:user:7:meta", "meta", meta); err != nil {
		t.Fatal(err)
	}
	if err := saver.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	m := newTestManager(t, Options{DB: db})
	if _, err := m.List(ctx, 7, []string{"A"}, 0, 100); err == nil {
		t.Fatal("unsupported cursors treated as a fresh mailbox")
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := rdb.LoadHash(ctx, db, "aifd:mail:user:7:meta", func() *mailpb.MailboxMeta { return &mailpb.MailboxMeta{} })
	if err != nil || stored["meta"] == nil || len(stored["meta"].ProtoReflect().GetUnknown()) == 0 {
		t.Fatal("unsupported metadata overwritten", stored, err)
	}
}
