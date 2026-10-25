package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	mail "github.com/li9chuan/aifirstdev/aifd-plugins/mail"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
)

func TestAwardPanicClearsInflightAndAllowsRetryAfterCD(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	var attempts atomic.Int64
	owner := newOwner(t, Options{Clock: clock, Awarder: mail.AwarderFunc(func(context.Context, int64, *mailpb.MailData) error {
		if attempts.Add(1) == 1 {
			panic("injected")
		}
		return nil
	})})
	id := send(t, owner, personal(7, true))
	req := &mailpb.MailIDsRequest{MailIdList: []int64{id}}
	if rsp := owner.Claim(context.Background(), 7, req); rsp.GetRet() != mail.RetInternal {
		t.Fatal(rsp.String())
	}
	clock.advance(5 * time.Second)
	if rsp := owner.Claim(context.Background(), 7, req); rsp.GetRet() != mail.RetOK || attempts.Load() != 2 {
		t.Fatal("panic stuck claim", rsp.String())
	}
}

func TestCloseDrainsAcceptedClaimDespiteCallerCancellation(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	owner := newOwner(t, Options{AwardTimeout: time.Second, Awarder: mail.AwarderFunc(func(ctx context.Context, _ int64, _ *mailpb.MailData) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})})
	id := send(t, owner, personal(7, true))
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan *mailpb.MseMailClaim, 1)
	go func() { result <- owner.Claim(ctx, 7, &mailpb.MailIDsRequest{MailIdList: []int64{id}}) }()
	<-entered
	cancel()
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	if err := owner.Close(closeCtx); err == nil {
		t.Fatal("close did not wait for accepted claim")
	}
	closeCancel()
	if rsp := owner.Add(context.Background(), personal(8, false)); rsp.GetRet() != mail.RetInternal {
		t.Fatal("closing owner admitted another operation")
	}
	close(release)
	if rsp := <-result; rsp.GetRet() != mail.RetOK {
		t.Fatal("caller cancellation dropped award result", rsp.String())
	}
	if err := owner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored, err := NewModule(Options{DB: owner.opts.DB})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close(context.Background())
	if err := restored.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	data := restored.Get(context.Background(), 7, &mailpb.MailGetRequest{MailId: id}).GetMail()
	if !data.GetClaimed() || !data.GetRead() {
		t.Fatal("drained result not persisted")
	}
}

func TestBatchAwardBudgetDoesNotApplyCDToUnstartedMails(t *testing.T) {
	owner := newOwner(t, Options{AwardTimeout: 20 * time.Millisecond, Awarder: mail.AwarderFunc(func(ctx context.Context, _ int64, _ *mailpb.MailData) error { <-ctx.Done(); return ctx.Err() })})
	first := send(t, owner, personal(7, true))
	second := send(t, owner, personal(7, true))
	rsp := owner.Claim(context.Background(), 7, &mailpb.MailIDsRequest{MailIdList: []int64{first, second}})
	if rsp.GetRet() != mail.RetTimeout {
		t.Fatal(rsp.String())
	}
	if data := owner.Get(context.Background(), 7, &mailpb.MailGetRequest{MailId: second}).GetMail(); data.GetClaimRetryAtUnixMs() != 0 {
		t.Fatal("unstarted mail acquired CD")
	}
}
