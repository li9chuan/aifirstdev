package store

import (
	"context"
	"testing"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
)

func TestMemoryStorePersonalFlow(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now().Unix()

	version := applyPersonal(t, s, 1001, 0, PersonalMailboxChange{
		Upsert:        []*mailpb.MailData{testMail(1, now+60), testMail(2, now+60)},
		MailCount:     2,
		DataExpiresAt: now + 60,
	})
	box, err := s.LoadPersonalMailbox(ctx, 1001)
	if err != nil {
		t.Fatalf("LoadPersonalMailbox error = %v", err)
	}
	if box.Version != version || len(box.Mails) != 2 {
		t.Fatalf("mailbox = %+v, want version %d and two mails", box, version)
	}

	data := testMail(2, now+60)
	data.Read = true
	version = applyPersonal(t, s, 1001, version, PersonalMailboxChange{Upsert: []*mailpb.MailData{data}, MailCount: 2, DataExpiresAt: now + 60})
	version = applyPersonal(t, s, 1001, version, PersonalMailboxChange{DeleteIDs: []int64{2, 3}, MailCount: 1, DataExpiresAt: now + 60})
	box, err = s.LoadPersonalMailbox(ctx, 1001)
	if err != nil || box.Version != version || len(box.Mails) != 1 || box.Mails[0].GetMailId() != 1 {
		t.Fatalf("mailbox after delete = %+v err=%v", box, err)
	}
}

func TestMemoryStoreSharedGlobalAndGroup(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now().Unix()

	global := testMail(10, now+60)
	global.Type = mailpb.MailType_MAIL_TYPE_GLOBAL
	global.TargetRealms = []int32{1}
	if err := s.SaveShared(ctx, global); err != nil {
		t.Fatalf("SaveShared global error = %v", err)
	}
	group := testMail(11, now+60)
	group.Type = mailpb.MailType_MAIL_TYPE_GROUP
	group.GroupKeys = []string{"vip"}
	if err := s.SaveShared(ctx, group); err != nil {
		t.Fatalf("SaveShared group error = %v", err)
	}

	globalList, err := s.ListGlobalAfter(ctx, 0, 1, 10)
	if err != nil {
		t.Fatalf("ListGlobalAfter error = %v", err)
	}
	if len(globalList) != 1 || globalList[0].GetMailId() != 10 {
		t.Fatalf("globalList = %+v, want mail 10", globalList)
	}
	if skipped, err := s.ListGlobalAfter(ctx, 0, 2, 10); err != nil || len(skipped) != 0 {
		t.Fatalf("realm skipped = %+v err=%v, want empty nil", skipped, err)
	}
	groupList, err := s.ListGroupAfter(ctx, "vip", 0, 10)
	if err != nil {
		t.Fatalf("ListGroupAfter error = %v", err)
	}
	if len(groupList) != 1 || groupList[0].GetMailId() != 11 {
		t.Fatalf("groupList = %+v, want mail 11", groupList)
	}
}

func TestMemoryStoreCleanupOnlyShared(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now().Unix()
	applyPersonal(t, s, 1001, 0, PersonalMailboxChange{Upsert: []*mailpb.MailData{testMail(1, now-1)}, MailCount: 1, DataExpiresAt: now - 1})
	shared := testMail(2, now-1)
	shared.Type = mailpb.MailType_MAIL_TYPE_GLOBAL
	if err := s.SaveShared(ctx, shared); err != nil {
		t.Fatalf("SaveShared error = %v", err)
	}
	count, err := s.CleanupSharedExpired(ctx, now)
	if err != nil {
		t.Fatalf("CleanupExpired error = %v", err)
	}
	if count != 1 {
		t.Fatalf("CleanupSharedExpired count = %d, want 1", count)
	}
	if box, _ := s.LoadPersonalMailbox(ctx, 1001); len(box.Mails) != 1 {
		t.Fatalf("personal mailbox = %+v, want manager-owned cleanup", box)
	}
}

func applyPersonal(t *testing.T, s Store, rootID int64, version uint64, change PersonalMailboxChange) uint64 {
	t.Helper()
	newVersion, err := s.ApplyPersonalMailbox(context.Background(), rootID, version, change)
	if err != nil {
		t.Fatalf("ApplyPersonalMailbox error = %v", err)
	}
	return newVersion
}

func testMail(id int64, end int64) *mailpb.MailData {
	return &mailpb.MailData{
		MailId:       id,
		Type:         mailpb.MailType_MAIL_TYPE_PERSONAL,
		Base:         &mailpb.MailBase{TemplateId: 1, ItemList: []*mailpb.MailItem{{Tid: 10, Num: 1}}},
		SendTimeUnix: time.Now().Unix(),
		EndTimeUnix:  end,
	}
}
