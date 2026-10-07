package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
	"github.com/alicebob/miniredis/v2"
	redisgo "github.com/redis/go-redis/v9"
)

func TestRedisStorePersonalApplyUsesCASAndPerUserTTL(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redisgo.NewClient(&redisgo.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	s, err := NewRedisStore(client, RedisStoreOptions{KeyPrefix: "test:mail"})
	if err != nil {
		t.Fatalf("NewRedisStore error = %v", err)
	}
	if err := client.ZAdd(ctx, s.userIndexKey(1001), redisgo.Z{Score: 1, Member: "1"}).Err(); err != nil {
		t.Fatalf("seed legacy index error = %v", err)
	}
	expiresAt := time.Now().Add(time.Hour).Unix()
	change := PersonalMailboxChange{Upsert: []*mailpb.MailData{testMail(1, expiresAt)}, MailCount: 1, DataExpiresAt: expiresAt}
	version, err := s.ApplyPersonalMailbox(ctx, 1001, 0, change)
	if err != nil || version != 1 {
		t.Fatalf("ApplyPersonalMailbox version=%d err=%v, want 1 nil", version, err)
	}
	if _, err := s.ApplyPersonalMailbox(ctx, 1001, 0, change); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale ApplyPersonalMailbox error = %v, want version conflict", err)
	}
	if ttl := server.TTL(s.userDataKey(1001)); ttl <= 0 {
		t.Fatalf("personal data ttl = %v, want positive", ttl)
	}
	if server.Exists(s.userIndexKey(1001)) {
		t.Fatal("legacy personal index still exists after versioned apply")
	}
}

func TestRedisStorePersonalAndSharedFlow(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redisgo.NewClient(&redisgo.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	s, err := NewRedisStore(client, RedisStoreOptions{KeyPrefix: "test:mail"})
	if err != nil {
		t.Fatalf("NewRedisStore error = %v", err)
	}
	now := time.Now().Unix()
	applyPersonal(t, s, 1001, 0, PersonalMailboxChange{Upsert: []*mailpb.MailData{testMail(1, now+60)}, MailCount: 1, DataExpiresAt: now + 60})
	personal, err := s.LoadPersonalMailbox(ctx, 1001)
	if err != nil {
		t.Fatalf("LoadPersonalMailbox error = %v", err)
	}
	if len(personal.Mails) != 1 || personal.Mails[0].GetMailId() != 1 {
		t.Fatalf("personal = %+v, want mail 1", personal)
	}
	custom := testMail(4, now+60)
	custom.Base.TemplateId = 0
	custom.Base.CustomContent = &mailpb.MailCustomContent{Title: "自定义标题", Body: "自定义正文"}
	applyPersonal(t, s, 1002, 0, PersonalMailboxChange{Upsert: []*mailpb.MailData{custom}, MailCount: 1, DataExpiresAt: now + 60})
	customBox, err := s.LoadPersonalMailbox(ctx, 1002)
	if err != nil {
		t.Fatalf("LoadPersonalMailbox custom error = %v", err)
	}
	if len(customBox.Mails) != 1 {
		t.Fatalf("custom mailbox = %+v, want one mail", customBox)
	}
	customContent := customBox.Mails[0].GetBase().GetCustomContent()
	if customContent.GetTitle() != "自定义标题" || customContent.GetBody() != "自定义正文" {
		t.Fatalf("custom mailbox = %+v, want persisted custom content", customBox)
	}

	global := testMail(2, now+60)
	global.Type = mailpb.MailType_MAIL_TYPE_GLOBAL
	if err := s.SaveShared(ctx, global); err != nil {
		t.Fatalf("SaveShared global error = %v", err)
	}
	group := testMail(3, now+60)
	group.Type = mailpb.MailType_MAIL_TYPE_GROUP
	group.GroupKeys = []string{"vip"}
	if err := s.SaveShared(ctx, group); err != nil {
		t.Fatalf("SaveShared group error = %v", err)
	}
	globalList, err := s.ListGlobalAfter(ctx, 0, 0, 10)
	if err != nil {
		t.Fatalf("ListGlobalAfter error = %v", err)
	}
	if len(globalList) != 1 || globalList[0].GetMailId() != 2 {
		t.Fatalf("globalList = %+v, want mail 2", globalList)
	}
	groupList, err := s.ListGroupAfter(ctx, "vip", 0, 10)
	if err != nil {
		t.Fatalf("ListGroupAfter error = %v", err)
	}
	if len(groupList) != 1 || groupList[0].GetMailId() != 3 {
		t.Fatalf("groupList = %+v, want mail 3", groupList)
	}
}

func TestRedisStoreMetaAndCleanup(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redisgo.NewClient(&redisgo.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	s, err := NewRedisStore(client, RedisStoreOptions{KeyPrefix: "test:mail"})
	if err != nil {
		t.Fatalf("NewRedisStore error = %v", err)
	}
	meta := &mailpb.MailboxMeta{LastGlobalMailId: 12, GroupCursors: []*mailpb.MailGroupCursor{{GroupKey: "vip", LastMailId: 3}}}
	applyPersonal(t, s, 1001, 0, PersonalMailboxChange{Meta: meta, MailCount: 0})
	box, err := s.LoadPersonalMailbox(ctx, 1001)
	if err != nil {
		t.Fatalf("LoadPersonalMailbox error = %v", err)
	}
	loaded := box.Meta
	if loaded.GetLastGlobalMailId() != 12 || loaded.GetGroupCursors()[0].GetLastMailId() != 3 {
		t.Fatalf("loaded meta = %+v, want saved values", loaded)
	}

	expired := testMail(5, time.Now().Unix()-1)
	expired.Type = mailpb.MailType_MAIL_TYPE_GLOBAL
	if err := s.SaveShared(ctx, expired); err != nil {
		t.Fatalf("SaveShared error = %v", err)
	}
	expiredPersonal := testMail(6, time.Now().Unix()-1)
	applyPersonal(t, s, 1001, box.Version, PersonalMailboxChange{Upsert: []*mailpb.MailData{expiredPersonal}, Meta: meta, MailCount: 1, DataExpiresAt: expiredPersonal.GetEndTimeUnix()})
	count, err := s.CleanupSharedExpired(ctx, time.Now().Unix())
	if err != nil {
		t.Fatalf("CleanupExpired error = %v", err)
	}
	if count != 1 {
		t.Fatalf("CleanupSharedExpired count = %d, want 1", count)
	}
	list, err := s.ListGlobalAfter(ctx, 0, 0, 10)
	if err != nil {
		t.Fatalf("ListGlobalAfter error = %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("global after cleanup = %+v, want empty", list)
	}
	personal, err := s.LoadPersonalMailbox(ctx, 1001)
	if err != nil {
		t.Fatalf("LoadPersonalMailbox error = %v", err)
	}
	if len(personal.Mails) != 0 {
		t.Fatalf("personal after cleanup = %+v, want empty", personal)
	}
}
