package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	mail "github.com/li9chuan/aifirstdev/aifd-plugins/mail"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/mailbox"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/store"
)

func TestServiceAddPersonalGlobalAndGroup(t *testing.T) {
	ctx := context.Background()
	dataStore := store.NewMemoryStore()
	module := newTestModule(t, dataStore, mailbox.Options{}, Options{
		IDGenerator:     &seqGenerator{},
		CleanupInterval: time.Hour,
	})
	if err := module.Recover(ctx); err != nil {
		t.Fatalf("Recover error = %v", err)
	}

	personalRsp := module.Add(ctx, &mailpb.MailAddRequest{
		Type:     mailpb.MailType_MAIL_TYPE_PERSONAL,
		Base:     &mailpb.MailBase{TemplateId: 1},
		UidList:  []int64{1001, 1002},
		SaveDays: 1,
	})
	if personalRsp.GetRet() != mail.RetOK || len(personalRsp.GetMailIdList()) != 2 {
		t.Fatalf("personal rsp = %+v, want two ids", personalRsp)
	}
	box, err := dataStore.LoadPersonalMailbox(ctx, 1001)
	if err != nil || len(box.Mails) != 1 || box.Mails[0].GetMailId() != personalRsp.GetMailIdList()[0] {
		t.Fatalf("personal mailbox=%+v err=%v, want added mail", box, err)
	}

	globalRsp := module.Add(ctx, &mailpb.MailAddRequest{
		Type:         mailpb.MailType_MAIL_TYPE_GLOBAL,
		Base:         &mailpb.MailBase{TemplateId: 2},
		TargetRealms: []int32{1},
	})
	if globalRsp.GetRet() != mail.RetOK {
		t.Fatalf("global rsp = %+v, want ok", globalRsp)
	}
	globalList, err := dataStore.ListGlobalAfter(ctx, 0, 1, 10)
	if err != nil {
		t.Fatalf("ListGlobalAfter error = %v", err)
	}
	if len(globalList) != 1 || globalList[0].GetMailId() != globalRsp.GetMailIdList()[0] {
		t.Fatalf("globalList = %+v, want added global mail", globalList)
	}

	groupRsp := module.Add(ctx, &mailpb.MailAddRequest{
		Type:      mailpb.MailType_MAIL_TYPE_GROUP,
		Base:      &mailpb.MailBase{TemplateId: 3},
		GroupKeys: []string{"vip"},
	})
	if groupRsp.GetRet() != mail.RetOK {
		t.Fatalf("group rsp = %+v, want ok", groupRsp)
	}
	groupList, err := dataStore.ListGroupAfter(ctx, "vip", 0, 10)
	if err != nil {
		t.Fatalf("ListGroupAfter error = %v", err)
	}
	if len(groupList) != 1 || groupList[0].GetMailId() != groupRsp.GetMailIdList()[0] {
		t.Fatalf("groupList = %+v, want added group mail", groupList)
	}
}

func TestServiceLifecycle(t *testing.T) {
	ctx := context.Background()
	module := newTestModule(t, store.NewMemoryStore(), mailbox.Options{}, Options{IDGenerator: &seqGenerator{}, CleanupInterval: time.Hour})
	if err := module.Recover(ctx); err != nil {
		t.Fatalf("Recover error = %v", err)
	}
	if err := module.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	if err := module.Close(ctx); err != nil {
		t.Fatalf("Close error = %v", err)
	}
}

func TestServiceAddCustomMail(t *testing.T) {
	ctx := context.Background()
	dataStore := store.NewMemoryStore()
	validatorCalled := false
	module := newTestModule(t, dataStore, mailbox.Options{}, Options{
		IDGenerator:     &seqGenerator{},
		CleanupInterval: time.Hour,
		Validator: mail.TemplateValidatorFunc(func(context.Context, *mailpb.MailBase) error {
			validatorCalled = true
			return nil
		}),
	})
	if err := module.Recover(ctx); err != nil {
		t.Fatalf("Recover error = %v", err)
	}

	customContent := &mailpb.MailCustomContent{Title: "系统通知", Body: "这是一封自定义邮件。"}
	rsp := module.Add(ctx, &mailpb.MailAddRequest{
		Type:    mailpb.MailType_MAIL_TYPE_PERSONAL,
		Base:    &mailpb.MailBase{CustomContent: customContent},
		UidList: []int64{1001},
	})
	if rsp.GetRet() != mail.RetOK || len(rsp.GetMailIdList()) != 1 {
		t.Fatalf("custom mail ret=%d ids=%v message=%q, want one id", rsp.GetRet(), rsp.GetMailIdList(), rsp.GetMessage())
	}
	if validatorCalled {
		t.Fatal("template validator was called for custom mail")
	}

	customContent.Title = "已被调用方修改"
	box, err := dataStore.LoadPersonalMailbox(ctx, 1001)
	if err != nil || len(box.Mails) != 1 {
		t.Fatalf("LoadPersonalMailbox box=%+v err=%v, want one mail", box, err)
	}
	data := box.Mails[0]
	if got := data.GetBase().GetCustomContent().GetTitle(); got != "系统通知" {
		t.Fatalf("custom title = %q, want %q", got, "系统通知")
	}
}

func TestServiceRejectsInvalidMailContent(t *testing.T) {
	tests := []struct {
		name    string
		base    *mailpb.MailBase
		message string
	}{
		{name: "missing content source", base: &mailpb.MailBase{}, message: "mail content requires template_id or custom_content"},
		{name: "negative template id", base: &mailpb.MailBase{TemplateId: -1}, message: "mail template_id cannot be negative"},
		{
			name:    "template and custom content",
			base:    &mailpb.MailBase{TemplateId: 1, CustomContent: &mailpb.MailCustomContent{Title: "标题", Body: "正文"}},
			message: "mail template and custom content are mutually exclusive",
		},
		{
			name:    "custom content with template arguments",
			base:    &mailpb.MailBase{ContentList: []string{"arg"}, CustomContent: &mailpb.MailCustomContent{Title: "标题", Body: "正文"}},
			message: "custom mail content_list must be empty",
		},
		{
			name:    "blank custom title",
			base:    &mailpb.MailBase{CustomContent: &mailpb.MailCustomContent{Title: "  ", Body: "正文"}},
			message: "custom mail title is required",
		},
		{
			name:    "blank custom body",
			base:    &mailpb.MailBase{CustomContent: &mailpb.MailCustomContent{Title: "标题", Body: "\t"}},
			message: "custom mail body is required",
		},
		{
			name:    "custom title too long",
			base:    &mailpb.MailBase{CustomContent: &mailpb.MailCustomContent{Title: strings.Repeat("界", maxCustomTitleRunes+1), Body: "正文"}},
			message: "custom mail title exceeds 100 characters",
		},
		{
			name:    "custom body too long",
			base:    &mailpb.MailBase{CustomContent: &mailpb.MailCustomContent{Title: "标题", Body: strings.Repeat("界", maxCustomBodyRunes+1)}},
			message: "custom mail body exceeds 5000 characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			module := newTestModule(t, store.NewMemoryStore(), mailbox.Options{}, Options{IDGenerator: &seqGenerator{}, CleanupInterval: time.Hour})
			if err := module.Recover(ctx); err != nil {
				t.Fatalf("Recover error = %v", err)
			}
			rsp := module.Add(ctx, &mailpb.MailAddRequest{
				Type:    mailpb.MailType_MAIL_TYPE_PERSONAL,
				Base:    tt.base,
				UidList: []int64{1001},
			})
			if rsp.GetRet() != mail.RetInvalid || rsp.GetMessage() != tt.message {
				t.Fatalf("ret=%d message=%q, want ret=%d message=%q", rsp.GetRet(), rsp.GetMessage(), mail.RetInvalid, tt.message)
			}
		})
	}
}

func TestServicePushesCompleteMailAndKeepsNewestLimit(t *testing.T) {
	ctx := context.Background()
	dataStore := store.NewMemoryStore()
	var pushed []*mailpb.MseMailReceived
	module := newTestModule(t, dataStore, mailbox.Options{MailboxLimit: 2}, Options{
		IDGenerator:     &seqGenerator{},
		CleanupInterval: time.Hour,
		Push: mail.MailPushPortFunc(func(_ context.Context, rootID int64, event *mailpb.MseMailReceived) error {
			if rootID != 1001 {
				t.Fatalf("push rootID = %d, want 1001", rootID)
			}
			pushed = append(pushed, event)
			return nil
		}),
	})
	if err := module.Recover(ctx); err != nil {
		t.Fatalf("Recover error = %v", err)
	}
	for i := 1; i <= 3; i++ {
		rsp := module.Add(ctx, &mailpb.MailAddRequest{
			Type:    mailpb.MailType_MAIL_TYPE_PERSONAL,
			UidList: []int64{1001},
			Base: &mailpb.MailBase{CustomContent: &mailpb.MailCustomContent{
				Title: "通知", Body: "完整正文",
			}},
		})
		if rsp.GetRet() != mail.RetOK {
			t.Fatalf("Add %d ret=%d message=%q", i, rsp.GetRet(), rsp.GetMessage())
		}
	}
	if len(pushed) != 3 || pushed[2].GetMail().GetBase().GetCustomContent().GetBody() != "完整正文" {
		t.Fatalf("pushed = %+v, want three complete mails", pushed)
	}
	box, err := dataStore.LoadPersonalMailbox(ctx, 1001)
	if err != nil {
		t.Fatalf("LoadPersonalMailbox error = %v", err)
	}
	// Durable mailbox loads do not promise map iteration order.
	ids := make([]int64, 0, len(box.Mails))
	for _, item := range box.Mails {
		ids = append(ids, item.GetMailId())
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []int64{2, 3}) {
		t.Fatalf("mail ids = %v, want newest [2 3]", ids)
	}
}

func TestServicePushFailureDoesNotRollbackMail(t *testing.T) {
	ctx := context.Background()
	dataStore := store.NewMemoryStore()
	module := newTestModule(t, dataStore, mailbox.Options{}, Options{
		IDGenerator:     &seqGenerator{},
		CleanupInterval: time.Hour,
		Push: mail.MailPushPortFunc(func(context.Context, int64, *mailpb.MseMailReceived) error {
			return errors.New("offline")
		}),
	})
	if err := module.Recover(ctx); err != nil {
		t.Fatalf("Recover error = %v", err)
	}
	rsp := module.Add(ctx, &mailpb.MailAddRequest{
		Type:    mailpb.MailType_MAIL_TYPE_PERSONAL,
		UidList: []int64{1001},
		Base:    &mailpb.MailBase{CustomContent: &mailpb.MailCustomContent{Title: "通知", Body: "正文"}},
	})
	if rsp.GetRet() != mail.RetOK || len(rsp.GetMailIdList()) != 1 {
		t.Fatalf("Add response = %+v, want stored despite push failure", rsp)
	}
	box, err := dataStore.LoadPersonalMailbox(ctx, 1001)
	if err != nil || len(box.Mails) != 1 {
		t.Fatalf("LoadPersonalMailbox box=%+v err=%v, want persisted", box, err)
	}
}

func newTestModule(t *testing.T, dataStore store.Store, managerOpts mailbox.Options, moduleOpts Options) *Module {
	t.Helper()
	managerOpts.Store = dataStore
	manager, err := mailbox.NewManager(managerOpts)
	if err != nil {
		t.Fatalf("NewManager error = %v", err)
	}
	moduleOpts.Mailbox = manager
	return NewModule(moduleOpts)
}

type seqGenerator struct {
	next int64
}

func (g *seqGenerator) NextID() (int64, error) {
	g.next++
	return g.next, nil
}
