package mailbox

import (
	"context"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/store"
	"testing"
	"time"
)

func TestIndependentManagersObserveSendReadAndDelete(t *testing.T) {
	ctx := context.Background()
	data := store.NewMemoryStore()
	first, err := NewManager(Options{Store: data})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewManager(Options{Store: data})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.List(7, 0, 100); err != nil {
		t.Fatal(err)
	}
	if err := first.Add(ctx, 7, &mailpb.MailData{MailId: 99, EndTimeUnix: time.Now().Add(72 * time.Hour).Unix(), Base: &mailpb.MailBase{CustomContent: &mailpb.MailCustomContent{Title: "report", Body: "full body"}}}); err != nil {
		t.Fatal(err)
	}
	found, ok, err := second.Get(7, 99)
	if err != nil || !ok || found.GetRead() {
		t.Fatalf("new mail: %v %v", ok, err)
	}
	if _, err := first.MarkRead(ctx, 7, []int64{99}); err != nil {
		t.Fatal(err)
	}
	found, ok, err = second.Get(7, 99)
	if err != nil || !ok || !found.GetRead() {
		t.Fatalf("read state: %v %v", ok, err)
	}
	if _, err := first.DeleteRead(ctx, 7, nil); err != nil {
		t.Fatal(err)
	}
	_, ok, err = second.Get(7, 99)
	if err != nil || ok {
		t.Fatalf("deleted mail: %v %v", ok, err)
	}
}
