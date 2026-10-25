package mailbox

import (
	"context"
	"fmt"
	"testing"
	"time"

	mail "github.com/li9chuan/aifirstdev/aifd-plugins/mail"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
)

func TestSharedSnapshotContainsOnlyGlobalAndRequestedGroups(t *testing.T) {
	m := newTestManager(t, Options{})
	for _, group := range []string{"", "A", "B"} {
		typ := mailpb.MailType_MAIL_TYPE_GROUP
		if group == "" {
			typ = mailpb.MailType_MAIL_TYPE_GLOBAL
		}
		if _, err := m.PublishShared(context.Background(), &mailpb.MailAddRequest{Type: typ, GroupKey: group, Base: &mailpb.MailBase{TemplateId: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := m.snapshotShared([]string{"A", "A"})
	if len(snapshot.global) != 1 || len(snapshot.groups) != 1 || len(snapshot.groups["A"]) != 1 {
		t.Fatal("unexpected or repeated streams", snapshot)
	}
	if _, exists := snapshot.groups["B"]; exists {
		t.Fatal("unrelated group copied")
	}
	if got := m.snapshotShared(nil); len(got.global) != 1 || len(got.groups) != 0 {
		t.Fatal("no-membership snapshot copied groups", got)
	}
}

func TestSingleMailOperationsDoNotAcquireSharedPoolLock(t *testing.T) {
	ctx := context.Background()
	m := newTestManager(t, Options{Awarder: mail.AwarderFunc(func(context.Context, int64, *mailpb.MailData) error { return nil })})
	plainReq := &mailpb.MailAddRequest{Type: mailpb.MailType_MAIL_TYPE_GROUP, GroupKey: "A", Base: &mailpb.MailBase{TemplateId: 1}}
	plain, err := m.PublishShared(ctx, plainReq)
	if err != nil {
		t.Fatal(err)
	}
	rewardReq := &mailpb.MailAddRequest{Type: mailpb.MailType_MAIL_TYPE_GROUP, GroupKey: "A", Base: &mailpb.MailBase{TemplateId: 1, ItemList: []*mailpb.MailItem{{Tid: 1, Num: 1}}}}
	reward, err := m.PublishShared(ctx, rewardReq)
	if err != nil {
		t.Fatal(err)
	}
	groups := []string{"A"}
	if err := m.SyncShared(ctx, 7, groups); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	m.shared.mu.Lock()
	go func() {
		if _, found, err := m.Get(ctx, 7, groups, plain.GetMailId()); err != nil || !found {
			done <- fmt.Errorf("get: found=%v err=%v", found, err)
			return
		}
		if _, found, err := m.Get(ctx, 7, nil, plain.GetMailId()); err != nil || found {
			done <- fmt.Errorf("nonmember get: found=%v err=%v", found, err)
			return
		}
		if ids, err := m.MarkRead(ctx, 7, groups, []int64{plain.GetMailId()}); err != nil || len(ids) != 1 {
			done <- fmt.Errorf("read: %v %v", ids, err)
			return
		}
		if result, err := m.Claim(ctx, 7, groups, []int64{reward.GetMailId()}); err != nil || len(result.MailIDs) != 1 {
			done <- fmt.Errorf("claim: %v %v", result, err)
			return
		}
		if ids, err := m.DeleteRead(ctx, 7, groups, []int64{plain.GetMailId(), reward.GetMailId()}); err != nil || len(ids) != 2 {
			done <- fmt.Errorf("delete: %v %v", ids, err)
			return
		}
		done <- nil
	}()
	select {
	case err := <-done:
		m.shared.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		m.shared.mu.Unlock()
		<-done
		t.Fatal("single-mail operation waited for an unrelated shared-pool writer")
	}
}

func BenchmarkSharedSnapshotUnrelatedGroups(b *testing.B) {
	for _, count := range []int{0, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			m := &Manager{Options: Options{Clock: mail.SystemClock{}}, shared: sharedState{groups: make(map[string][]*mailpb.MailData)}}
			for i := range count {
				indexShared(&m.shared, &mailpb.MailData{MailId: int64(i + 1), Type: mailpb.MailType_MAIL_TYPE_GROUP, GroupKey: fmt.Sprintf("unrelated:%d", i)})
			}
			for i := range 10 {
				indexShared(&m.shared, &mailpb.MailData{MailId: int64(count + i + 1), Type: mailpb.MailType_MAIL_TYPE_GLOBAL})
				indexShared(&m.shared, &mailpb.MailData{MailId: int64(count + i + 11), Type: mailpb.MailType_MAIL_TYPE_GROUP, GroupKey: "selected"})
			}
			groups := []string{"selected", "selected"}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				snapshot := m.snapshotShared(groups)
				if len(snapshot.global) != 10 || len(snapshot.groups) != 1 || len(snapshot.groups["selected"]) != 10 {
					b.Fatal("wrong selected snapshot")
				}
			}
		})
	}
}
