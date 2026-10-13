package root

import (
	"context"
	"testing"
	"time"

	mail "github.com/li9chuan/aifirstdev/aifd-plugins/mail"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/mailbox"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/store"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestRootListSyncsGlobalAndGroup(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()
	now := time.Now().Unix()
	global := rootTestMail(10, now+60)
	global.Type = mailpb.MailType_MAIL_TYPE_GLOBAL
	global.TargetRealms = []int32{1}
	if err := mem.SaveShared(ctx, global); err != nil {
		t.Fatalf("SaveShared global error = %v", err)
	}
	group := rootTestMail(11, now+60)
	group.Type = mailpb.MailType_MAIL_TYPE_GROUP
	group.GroupKeys = []string{"vip"}
	if err := mem.SaveShared(ctx, group); err != nil {
		t.Fatalf("SaveShared group error = %v", err)
	}

	manager := newRootManager(t, mem)
	module := NewModule[*wrapperspb.StringValue](Options[*wrapperspb.StringValue]{Mailbox: manager, RealmID: 1})
	rsp := module.List(ctx, 1001, nil, &mailpb.MceMailList{GroupKeys: []string{"vip"}})
	if rsp.GetRet() != mail.RetOK {
		t.Fatalf("List ret=%d message=%q, want ok", rsp.GetRet(), rsp.GetMessage())
	}
	if got := mailIDs(rsp.GetMailList()); len(got) != 2 || got[0] != 10 || got[1] != 11 {
		t.Fatalf("mail ids = %v, want [10 11]", got)
	}

	manager.Invalidate(1001)
	rsp = module.List(ctx, 1001, nil, &mailpb.MceMailList{GroupKeys: []string{"vip"}})
	if got := mailIDs(rsp.GetMailList()); len(got) != 2 || got[0] != 10 || got[1] != 11 {
		t.Fatalf("mail ids after cache eviction = %v, want [10 11]", got)
	}
}

func TestRootClaimIsIdempotent(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()
	manager := newRootManager(t, mem)
	if err := manager.Add(ctx, 1001, rootTestMail(1, time.Now().Unix()+60)); err != nil {
		t.Fatalf("Add error = %v", err)
	}
	var awards int
	module := NewModule[*wrapperspb.StringValue](Options[*wrapperspb.StringValue]{
		Mailbox: manager,
		Awarder: mail.AwarderFunc(func(context.Context, int64, *mailpb.MailData) error {
			awards++
			return nil
		}),
	})

	first := module.Claim(ctx, 1001, &mailpb.MceMailClaim{MailIdList: []int64{1}})
	if first.GetRet() != mail.RetOK || len(first.GetMailIdList()) != 1 {
		t.Fatalf("first claim = %+v, want one claimed id", first)
	}
	second := module.Claim(ctx, 1001, &mailpb.MceMailClaim{MailIdList: []int64{1}})
	if second.GetRet() != mail.RetOK || len(second.GetMailIdList()) != 0 {
		t.Fatalf("second claim = %+v, want no claimed id", second)
	}
	if awards != 1 {
		t.Fatalf("awards = %d, want 1", awards)
	}
}

func TestRootReadAndDeleteRead(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()
	noAttachment := rootTestMail(1, time.Now().Unix()+60)
	noAttachment.Base.ItemList = nil
	manager := newRootManager(t, mem)
	if err := manager.Add(ctx, 1001, noAttachment); err != nil {
		t.Fatalf("Add noAttachment error = %v", err)
	}
	withAttachment := rootTestMail(2, time.Now().Unix()+60)
	if err := manager.Add(ctx, 1001, withAttachment); err != nil {
		t.Fatalf("Add withAttachment error = %v", err)
	}
	module := NewModule[*wrapperspb.StringValue](Options[*wrapperspb.StringValue]{Mailbox: manager})

	read := module.Read(ctx, 1001, &mailpb.MceMailRead{MailIdList: []int64{1, 2}})
	if got := read.GetMailIdList(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("read ids = %v, want [1]", got)
	}
	deleted := module.DeleteRead(ctx, 1001, &mailpb.MceMailDeleteRead{})
	if got := deleted.GetMailIdList(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("deleted ids = %v, want [1]", got)
	}
	if _, ok, err := manager.Get(1001, 2); err != nil || !ok {
		t.Fatalf("mail 2 ok=%v err=%v, want true nil", ok, err)
	}
}

func newRootManager(t *testing.T, dataStore store.Store) *mailbox.Manager {
	t.Helper()
	manager, err := mailbox.NewManager(mailbox.Options{Store: dataStore})
	if err != nil {
		t.Fatalf("NewManager error = %v", err)
	}
	return manager
}

func rootTestMail(id int64, end int64) *mailpb.MailData {
	return &mailpb.MailData{
		MailId:       id,
		Type:         mailpb.MailType_MAIL_TYPE_PERSONAL,
		Base:         &mailpb.MailBase{TemplateId: 1, ItemList: []*mailpb.MailItem{{Tid: 10, Num: 1}}},
		SendTimeUnix: time.Now().Unix(),
		EndTimeUnix:  end,
	}
}

func mailIDs(list []*mailpb.MailData) []int64 {
	out := make([]int64, 0, len(list))
	for _, item := range list {
		out = append(out, item.GetMailId())
	}
	return out
}
