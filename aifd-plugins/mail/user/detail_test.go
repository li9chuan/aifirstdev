package user

import (
	"context"
	mail "github.com/li9chuan/aifirstdev/aifd-plugins/mail"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/mailbox"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/service"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/store"
	"google.golang.org/protobuf/types/known/emptypb"
	"strings"
	"testing"
	"time"
)

func TestLongMailSummaryDetailOwnershipAndExpiry(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	clock := mail.ClockFunc(func() time.Time { return now })
	manager, err := mailbox.NewManager(mailbox.Options{Store: store.NewMemoryStore(), Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	sender := service.NewModule(service.Options{Mailbox: manager, Clock: clock, MaxBodyRunes: 20000})
	if err := sender.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("长正文", 4000)
	sent := sender.Add(ctx, &mailpb.MailAddRequest{Type: mailpb.MailType_MAIL_TYPE_PERSONAL, UidList: []int64{7}, SaveDays: 3, Base: &mailpb.MailBase{CustomContent: &mailpb.MailCustomContent{Title: "周报", Body: body}}})
	if sent.GetRet() != mail.RetOK {
		t.Fatal(sent.String())
	}
	id := sent.GetMailIdList()[0]
	user := NewModule[*emptypb.Empty](Options[*emptypb.Empty]{Mailbox: manager, Clock: clock})
	list := user.List(ctx, 7, nil, &mailpb.MceMailList{SummaryOnly: true})
	if len(list.GetMailList()) != 1 || list.MailList[0].GetBase().GetCustomContent().GetBody() != "" || len([]rune(list.MailList[0].GetPreview())) != 160 {
		t.Fatal("list leaked or lost long body")
	}
	result := user.Get(ctx, 7, &mailpb.MceMailGet{MailId: id})
	if result.GetMail().GetBase().GetCustomContent().GetBody() != body || result.GetMail().GetRead() {
		t.Fatal("detail changed body/read state")
	}
	if result.GetMail().GetEndTimeUnix() != now.Add(72*time.Hour).Unix() {
		t.Fatal("expiry mismatch")
	}
	if user.Get(ctx, 8, &mailpb.MceMailGet{MailId: id}).GetRet() != mail.RetNotFound {
		t.Fatal("cross-user access")
	}
	now = now.Add(72 * time.Hour)
	if user.Get(ctx, 7, &mailpb.MceMailGet{MailId: id}).GetRet() != mail.RetNotFound {
		t.Fatal("expired body returned")
	}
}
