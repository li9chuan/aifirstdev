package store

import (
	"testing"

	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
)

func TestCloneMailDeepCopiesCustomContent(t *testing.T) {
	original := &mailpb.MailData{
		MailId: 1,
		Base: &mailpb.MailBase{
			CustomContent: &mailpb.MailCustomContent{Title: "原始标题", Body: "原始正文"},
		},
	}

	cloned := CloneMail(original)
	cloned.Base.CustomContent.Title = "修改后的标题"
	cloned.Base.CustomContent.Body = "修改后的正文"

	if got := original.GetBase().GetCustomContent().GetTitle(); got != "原始标题" {
		t.Fatalf("original title = %q, want %q", got, "原始标题")
	}
	if got := original.GetBase().GetCustomContent().GetBody(); got != "原始正文" {
		t.Fatalf("original body = %q, want %q", got, "原始正文")
	}
}
