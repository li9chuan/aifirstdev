package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mail "github.com/li9chuan/aifirstdev/aifd-plugins/mail"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
)

type seqGenerator struct {
	next   atomic.Int64
	failAt int64
}

func (g *seqGenerator) NextID() (int64, error) {
	n := g.next.Add(1)
	if n == g.failAt {
		return 0, errors.New("injected ID failure")
	}
	return n, nil
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) advance(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

func newOwner(t *testing.T, opts Options) *Module {
	t.Helper()
	if opts.DB == nil {
		opts.DB = rdb.MustNewMemoryDB(rdb.WithSaveDelay(time.Hour, time.Hour))
		t.Cleanup(func() {
			if err := opts.DB.Close(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	if opts.IDGenerator == nil {
		opts.IDGenerator = &seqGenerator{}
	}
	m, err := NewModule(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := m.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return m
}
func personal(uid int64, items bool) *mailpb.MailAddRequest {
	base := &mailpb.MailBase{TemplateId: 1}
	if items {
		base.ItemList = []*mailpb.MailItem{{Tid: 1, Num: 2}}
	}
	return &mailpb.MailAddRequest{Type: mailpb.MailType_MAIL_TYPE_PERSONAL, Base: base, UidList: []int64{uid}}
}
func shared(group string) *mailpb.MailAddRequest {
	typ := mailpb.MailType_MAIL_TYPE_GLOBAL
	if group != "" {
		typ = mailpb.MailType_MAIL_TYPE_GROUP
	}
	return &mailpb.MailAddRequest{Type: typ, Base: &mailpb.MailBase{TemplateId: 1}, GroupKey: group}
}
func send(t *testing.T, m *Module, req *mailpb.MailAddRequest) int64 {
	t.Helper()
	rsp := m.Add(context.Background(), req)
	if rsp.GetRet() != mail.RetOK || len(rsp.GetMailIdList()) == 0 {
		t.Fatal(rsp.String())
	}
	return rsp.MailIdList[0]
}
func list(t *testing.T, m *Module, uid int64, groups []string) []*mailpb.MailData {
	t.Helper()
	rsp := m.List(context.Background(), uid, &mailpb.MailListRequest{GroupKeys: groups, Query: &mailpb.MceMailList{Limit: 1000}})
	if rsp.GetRet() != mail.RetOK {
		t.Fatal(rsp.String())
	}
	return rsp.GetMailList()
}

func TestSingleGroupTransitionsPreserveStateAndDeletion(t *testing.T) {
	ctx := context.Background()
	m := newOwner(t, Options{})
	l0, l1 := []string{"L0"}, []string{"L1"}
	id := send(t, m, shared("L0"))
	otherID := send(t, m, shared("L1"))
	if len(list(t, m, 7, nil)) != 0 {
		t.Fatal("nonmember received group mail")
	}
	if got := list(t, m, 7, l0); len(got) != 1 || got[0].GetMailId() != id {
		t.Fatal("wrong L0 delivery")
	}
	if rsp := m.Read(ctx, 7, &mailpb.MailIDsRequest{GroupKeys: l0, MailIdList: []int64{id}}); rsp.GetRet() != mail.RetOK {
		t.Fatal(rsp.String())
	}
	if got := list(t, m, 7, l1); len(got) != 1 || got[0].GetMailId() != otherID {
		t.Fatal("wrong group switch delivery")
	}
	if m.Get(ctx, 7, &mailpb.MailGetRequest{GroupKeys: l1, MailId: id}).GetRet() != mail.RetNotFound {
		t.Fatal("detail bypassed group membership")
	}
	if got := list(t, m, 7, l0); len(got) != 1 || !got[0].GetRead() {
		t.Fatal("rejoining reset read state")
	}
	if rsp := m.DeleteRead(ctx, 7, &mailpb.MailIDsRequest{GroupKeys: l0, MailIdList: []int64{id}}); len(rsp.GetMailIdList()) != 1 {
		t.Fatal(rsp.String())
	}
	list(t, m, 7, l1)
	if len(list(t, m, 7, l0)) != 0 {
		t.Fatal("deleted mail resurrected on rejoin")
	}
	if len(list(t, m, 8, l0)) != 1 {
		t.Fatal("new member missed unexpired historical mail")
	}
}

func TestGroupBatchSendsIndependentMailsToOverlappingMembers(t *testing.T) {
	var awards atomic.Int64
	m := newOwner(t, Options{Awarder: mail.AwarderFunc(func(context.Context, int64, *mailpb.MailData) error { awards.Add(1); return nil })})
	first, second := shared("L1"), shared("server:1")
	first.Base.ItemList = []*mailpb.MailItem{{Tid: 1, Num: 1}}
	second.Base.ItemList = []*mailpb.MailItem{{Tid: 1, Num: 1}}
	rsp := m.AddBatch(context.Background(), &mailpb.MailAddBatchRequest{MailList: []*mailpb.MailAddRequest{first, second}})
	if rsp.GetRet() != mail.RetOK || len(rsp.GetMailIdList()) != 2 || rsp.MailIdList[0] == rsp.MailIdList[1] {
		t.Fatal(rsp.String())
	}
	groups := []string{"L1", "server:1", "L1"}
	got := list(t, m, 7, groups)
	if len(got) != 2 || got[0].GetGroupKey() != "L1" || got[1].GetGroupKey() != "server:1" {
		t.Fatal("independent groups collapsed")
	}
	if claimed := m.Claim(context.Background(), 7, &mailpb.MailIDsRequest{GroupKeys: groups, MailIdList: rsp.GetMailIdList()}); claimed.GetRet() != mail.RetOK || len(claimed.GetMailIdList()) != 2 || awards.Load() != 2 {
		t.Fatal(claimed.String(), awards.Load())
	}
	if len(list(t, m, 8, []string{"L1"})) != 1 || len(list(t, m, 9, []string{"server:1"})) != 1 || len(list(t, m, 10, nil)) != 0 {
		t.Fatal("group names were interpreted as combined conditions")
	}
	send(t, m, shared("L1|server:1"))
	if len(list(t, m, 8, []string{"L1", "server:1"})) != 2 || len(list(t, m, 11, []string{"L1|server:1"})) != 1 {
		t.Fatal("group key was parsed as an expression")
	}
	send(t, m, shared(""))
	if len(list(t, m, 10, nil)) != 1 {
		t.Fatal("GLOBAL acquired a membership restriction")
	}
}

func TestSingleGroupSendRejectsLegacyTargets(t *testing.T) {
	m := newOwner(t, Options{})
	invalid := []*mailpb.MailAddRequest{
		{Type: mailpb.MailType_MAIL_TYPE_GROUP, Base: &mailpb.MailBase{TemplateId: 1}},
		{Type: mailpb.MailType_MAIL_TYPE_GROUP, Base: &mailpb.MailBase{TemplateId: 1}, GroupKey: " "},
		{Type: mailpb.MailType_MAIL_TYPE_GROUP, Base: &mailpb.MailBase{TemplateId: 1}, GroupKeys: []string{"A", "B"}},
		{Type: mailpb.MailType_MAIL_TYPE_GROUP, Base: &mailpb.MailBase{TemplateId: 1}, GroupKey: "server:1", TargetRealms: []int32{1}},
		{Type: mailpb.MailType_MAIL_TYPE_GLOBAL, Base: &mailpb.MailBase{TemplateId: 1}, TargetRealms: []int32{1}},
		{Type: mailpb.MailType_MAIL_TYPE_PERSONAL, Base: &mailpb.MailBase{TemplateId: 1}, UidList: []int64{7}, GroupKey: "A"},
	}
	for _, req := range invalid {
		if rsp := m.Add(context.Background(), req); rsp.GetRet() != mail.RetInvalid {
			t.Fatal("legacy/invalid targeting accepted", rsp.String())
		}
	}
	good := shared("A")
	if rsp := m.AddBatch(context.Background(), &mailpb.MailAddBatchRequest{MailList: []*mailpb.MailAddRequest{good, invalid[2]}}); rsp.GetRet() != mail.RetInvalid || len(rsp.GetMailIdList()) != 0 {
		t.Fatal(rsp.String())
	}
	if len(list(t, m, 7, []string{"A", "B", "server:1"})) != 0 {
		t.Fatal("invalid batch published prefix")
	}
}

func TestSharedCompleteBacklogInt64AndConcurrentPublication(t *testing.T) {
	gen := &seqGenerator{}
	gen.next.Store(1 << 60)
	m := newOwner(t, Options{IDGenerator: gen, MailboxLimit: 1000})
	for range 450 {
		send(t, m, shared("server:2"))
	}
	for range 101 {
		send(t, m, shared("server:1"))
	}
	groups := []string{"server:1"}
	got := list(t, m, 7, groups)
	if len(got) != 101 {
		t.Fatalf("got %d mails", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].GetMailId() != got[i-1].GetMailId()+1 {
			t.Fatal("int64 adjacency lost")
		}
	}
	var wg sync.WaitGroup
	for range 40 {
		wg.Go(func() {
			if rsp := m.Add(context.Background(), shared("server:1")); rsp.GetRet() != mail.RetOK {
				t.Error(rsp.String())
			}
		})
	}
	for range 20 {
		wg.Go(func() {
			if rsp := m.List(context.Background(), 7, &mailpb.MailListRequest{GroupKeys: groups}); rsp.GetRet() != mail.RetOK {
				t.Error(rsp.String())
			}
		})
	}
	wg.Wait()
	if len(list(t, m, 7, groups)) != 141 {
		t.Fatal("concurrent publication lost mail")
	}
	send(t, m, shared("server:1"))
	if len(list(t, m, 7, groups)) != 142 {
		t.Fatal("mailbox stayed permanently synced")
	}
}

func TestClaimTimeoutCDAndOrdinaryAwardRetry(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	var awards atomic.Int64
	m := newOwner(t, Options{Clock: clock, AwardTimeout: 30 * time.Millisecond, Awarder: mail.AwarderFunc(func(ctx context.Context, _ int64, _ *mailpb.MailData) error {
		if awards.Add(1) == 1 {
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	})})
	id := send(t, m, personal(7, true))
	req := &mailpb.MailIDsRequest{MailIdList: []int64{id, id}}
	rsp := m.Claim(context.Background(), 7, req)
	if rsp.GetRet() != mail.RetTimeout || rsp.GetRetryAtUnixMs() != clock.Now().Add(5*time.Second).UnixMilli() {
		t.Fatal(rsp.String())
	}
	if data := m.Get(context.Background(), 7, &mailpb.MailGetRequest{MailId: id}).GetMail(); data.GetClaimed() || data.GetRead() {
		t.Fatal("timeout marked claimed")
	}
	if rsp = m.Claim(context.Background(), 7, req); rsp.GetRet() != mail.RetBusy || awards.Load() != 1 {
		t.Fatal("CD did not block retry")
	}
	clock.advance(5 * time.Second)
	if rsp = m.Claim(context.Background(), 7, req); rsp.GetRet() != mail.RetOK || len(rsp.GetMailIdList()) != 1 || awards.Load() != 2 {
		t.Fatal(rsp.String())
	}
	if rsp = m.Claim(context.Background(), 7, req); rsp.GetRet() != mail.RetOK || len(rsp.GetMailIdList()) != 0 || awards.Load() != 2 {
		t.Fatal("completed mail awarded twice")
	}
}

func TestGroupClaimCDAndStateSurviveLeavingAndRejoining(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	var awards atomic.Int64
	m := newOwner(t, Options{Clock: clock, Awarder: mail.AwarderFunc(func(context.Context, int64, *mailpb.MailData) error {
		if awards.Add(1) == 1 {
			return errors.New("ordinary award failed")
		}
		return nil
	})})
	req := shared("A")
	req.Base.ItemList = []*mailpb.MailItem{{Tid: 1, Num: 1}}
	id := send(t, m, req)
	list(t, m, 7, []string{"A"})
	claim := &mailpb.MailIDsRequest{GroupKeys: []string{"A"}, MailIdList: []int64{id}}
	failed := m.Claim(context.Background(), 7, claim)
	if failed.GetRet() != mail.RetInternal || failed.GetRetryAtUnixMs() == 0 {
		t.Fatal(failed.String())
	}
	if len(list(t, m, 7, []string{"B"})) != 0 {
		t.Fatal("departed group mail stayed visible")
	}
	if rsp := m.Claim(context.Background(), 7, &mailpb.MailIDsRequest{GroupKeys: []string{"B"}, MailIdList: []int64{id}}); rsp.GetRet() != mail.RetNotFound || awards.Load() != 1 {
		t.Fatal("nonmember awarded", rsp.String())
	}
	got := list(t, m, 7, []string{"A"})
	if len(got) != 1 || got[0].GetClaimRetryAtUnixMs() != failed.GetRetryAtUnixMs() || got[0].GetClaimed() {
		t.Fatal("rejoin reset failed claim state", got)
	}
	if rsp := m.Claim(context.Background(), 7, claim); rsp.GetRet() != mail.RetBusy || awards.Load() != 1 {
		t.Fatal("rejoining bypassed CD", rsp.String())
	}
	clock.advance(5 * time.Second)
	if rsp := m.Claim(context.Background(), 7, claim); rsp.GetRet() != mail.RetOK || awards.Load() != 2 {
		t.Fatal(rsp.String())
	}
	list(t, m, 7, nil)
	got = list(t, m, 7, []string{"A"})
	if len(got) != 1 || !got[0].GetRead() || !got[0].GetClaimed() {
		t.Fatal("rejoin reset successful claim state", got)
	}
	if rsp := m.Claim(context.Background(), 7, claim); rsp.GetRet() != mail.RetOK || len(rsp.GetMailIdList()) != 0 || awards.Load() != 2 {
		t.Fatal("rejoin re-awarded completed mail", rsp.String())
	}
}

func TestClaimInFlightBlocksElapsedCDAndDoesNotHoldMailboxLock(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	entered := make(chan struct{})
	release := make(chan struct{})
	var m *Module
	m = newOwner(t, Options{Clock: clock, MailboxLimit: 1, AwardTimeout: time.Second, Awarder: mail.AwarderFunc(func(ctx context.Context, uid int64, data *mailpb.MailData) error {
		if m.Get(ctx, uid, &mailpb.MailGetRequest{MailId: data.GetMailId()}).GetRet() != mail.RetOK {
			return errors.New("mail lock held during callback")
		}
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})})
	id := send(t, m, personal(7, true))
	req := &mailpb.MailIDsRequest{MailIdList: []int64{id}}
	result := make(chan *mailpb.MseMailClaim, 1)
	go func() { result <- m.Claim(context.Background(), 7, req) }()
	<-entered
	clock.advance(6 * time.Second)
	if rsp := m.Claim(context.Background(), 7, req); rsp.GetRet() != mail.RetBusy {
		t.Fatal(rsp.String())
	}
	newest := send(t, m, personal(7, false))
	if m.Get(context.Background(), 7, &mailpb.MailGetRequest{MailId: id}).GetRet() != mail.RetOK {
		t.Fatal("in-flight mail trimmed")
	}
	close(release)
	if rsp := <-result; rsp.GetRet() != mail.RetOK {
		t.Fatal(rsp.String())
	}
	if got := list(t, m, 7, nil); len(got) != 1 || got[0].GetMailId() != newest {
		t.Fatal("capacity not restored")
	}
}

func TestClaimMissingAwarderAndPartialResults(t *testing.T) {
	for _, awarder := range []mail.Awarder{nil, mail.AwarderFunc(nil)} {
		m := newOwner(t, Options{Awarder: awarder})
		id := send(t, m, personal(7, true))
		if m.Claim(context.Background(), 7, &mailpb.MailIDsRequest{MailIdList: []int64{id}}).GetRet() != mail.RetInternal {
			t.Fatal("missing awarder accepted")
		}
		data := m.Get(context.Background(), 7, &mailpb.MailGetRequest{MailId: id}).GetMail()
		if data.GetClaimed() || data.GetClaimRetryAtUnixMs() != 0 {
			t.Fatal("missing awarder changed mail")
		}
	}
	m := newOwner(t, Options{Awarder: mail.AwarderFunc(func(_ context.Context, _ int64, data *mailpb.MailData) error {
		if data.GetMailId() == 2 {
			return errors.New("business failure")
		}
		return nil
	})})
	first := send(t, m, personal(7, true))
	second := send(t, m, personal(7, true))
	rsp := m.Claim(context.Background(), 7, &mailpb.MailIDsRequest{MailIdList: []int64{first, second}})
	if rsp.GetRet() != mail.RetPartial || len(rsp.GetMailIdList()) != 1 || rsp.GetMailIdList()[0] != first || rsp.GetRetryAtUnixMs() == 0 {
		t.Fatal(rsp.String())
	}
}

func TestSendingPrefixValidationUIDDedupAndPush(t *testing.T) {
	var pushes atomic.Int64
	m := newOwner(t, Options{IDGenerator: &seqGenerator{failAt: 2}, Push: mail.MailPushPortFunc(func(_ context.Context, _ int64, _ *mailpb.MseMailReceived) error {
		pushes.Add(1)
		return errors.New("offline")
	})})
	req := personal(7, false)
	req.UidList = []int64{7, 7, 8}
	rsp := m.Add(context.Background(), req)
	if rsp.GetRet() != mail.RetPartial || len(rsp.GetMailIdList()) != 1 || pushes.Load() != 1 {
		t.Fatal(rsp.String())
	}
	if len(list(t, m, 7, nil)) != 1 || len(list(t, m, 8, nil)) != 0 {
		t.Fatal("wrong target prefix")
	}
	m2 := newOwner(t, Options{IDGenerator: &seqGenerator{failAt: 3}})
	rsp = m2.AddBatch(context.Background(), &mailpb.MailAddBatchRequest{MailList: []*mailpb.MailAddRequest{shared(""), personal(7, false), personal(8, false)}})
	if rsp.GetRet() != mail.RetPartial || fmt.Sprint(rsp.GetMailIdList()) != "[1 2]" {
		t.Fatal(rsp.String())
	}
	invalid := personal(9, false)
	invalid.UidList = []int64{9, -1}
	if rsp = m2.Add(context.Background(), invalid); rsp.GetRet() != mail.RetInvalid {
		t.Fatal("invalid UID silently ignored")
	}
	m3 := newOwner(t, Options{IDGenerator: &seqGenerator{failAt: 2}})
	rsp = m3.AddBatch(context.Background(), &mailpb.MailAddBatchRequest{MailList: []*mailpb.MailAddRequest{shared("A"), shared("B")}})
	if rsp.GetRet() != mail.RetPartial || fmt.Sprint(rsp.GetMailIdList()) != "[1]" {
		t.Fatal("group batch lost accepted prefix", rsp.String())
	}
	got := list(t, m3, 7, []string{"A", "B"})
	if len(got) != 1 || got[0].GetGroupKey() != "A" {
		t.Fatal("group batch failed outside expected prefix", got)
	}
}

func TestLongBodySummaryOwnershipExpiryAndContentValidation(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	m := newOwner(t, Options{Clock: clock, MaxBodyRunes: 12000})
	body := strings.Repeat("界", 11000)
	req := personal(7, false)
	req.SaveDays = 3
	req.Base = &mailpb.MailBase{CustomContent: &mailpb.MailCustomContent{Title: "周报", Body: body}}
	id := send(t, m, req)
	rsp := m.List(context.Background(), 7, &mailpb.MailListRequest{Query: &mailpb.MceMailList{SummaryOnly: true}})
	data := rsp.GetMailList()[0]
	if data.GetBase().GetCustomContent().GetBody() != "" || len([]rune(data.GetPreview())) != 160 {
		t.Fatal("summary leaked body")
	}
	detail := m.Get(context.Background(), 7, &mailpb.MailGetRequest{MailId: id}).GetMail()
	if detail.GetBase().GetCustomContent().GetBody() != body || detail.GetRead() {
		t.Fatal("detail changed body/read")
	}
	detail.Base.CustomContent.Body = "changed"
	if m.Get(context.Background(), 7, &mailpb.MailGetRequest{MailId: id}).GetMail().GetBase().GetCustomContent().GetBody() != body {
		t.Fatal("internal PB escaped")
	}
	if m.Get(context.Background(), 8, &mailpb.MailGetRequest{MailId: id}).GetRet() != mail.RetNotFound {
		t.Fatal("cross UID access")
	}
	clock.advance(72 * time.Hour)
	if m.Get(context.Background(), 7, &mailpb.MailGetRequest{MailId: id}).GetRet() != mail.RetNotFound {
		t.Fatal("expired body visible")
	}
	invalid := []*mailpb.MailBase{nil, {}, {TemplateId: -1}, {TemplateId: 1, CustomContent: &mailpb.MailCustomContent{}},
		{CustomContent: &mailpb.MailCustomContent{Title: "", Body: "body"}}, {CustomContent: &mailpb.MailCustomContent{Title: "title", Body: ""}},
		{CustomContent: &mailpb.MailCustomContent{Title: strings.Repeat("界", 101), Body: "body"}},
		{CustomContent: &mailpb.MailCustomContent{Title: "title", Body: strings.Repeat("界", 5001)}},
		{ContentList: []string{"arg"}, CustomContent: &mailpb.MailCustomContent{Title: "title", Body: "body"}}}
	for i, base := range invalid {
		if err := validateMailBase(base); err == nil {
			t.Errorf("invalid content %d accepted", i)
		}
	}
}

func TestLifecycleRejectsBeforeRecoverAndAfterClose(t *testing.T) {
	db := rdb.MustNewMemoryDB()
	defer db.Close(context.Background())
	m, err := NewModule(Options{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	if rsp := m.Add(context.Background(), personal(7, false)); rsp.GetRet() != mail.RetInternal {
		t.Fatal("accepted before recovery")
	}
	if err = m.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rsp := m.Add(context.Background(), personal(7, false)); rsp.GetRet() != mail.RetInternal {
		t.Fatal("accepted after close")
	}
	for _, opts := range []Options{{DB: db, ClaimCD: -1}, {DB: db, CacheCapacity: -1}, {DB: db, CleanupInterval: -1}, {DB: db, NodeID: 1024}} {
		if _, err := NewModule(opts); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
}

func TestDeleteAllReadKeepsUnclaimedAttachments(t *testing.T) {
	m := newOwner(t, Options{})
	plain := send(t, m, personal(7, false))
	reward := send(t, m, personal(7, true))
	read := m.Read(context.Background(), 7, &mailpb.MailIDsRequest{MailIdList: []int64{plain, reward}})
	if read.GetRet() != mail.RetOK || len(read.GetMailIdList()) != 1 || read.GetMailIdList()[0] != plain {
		t.Fatal(read.String())
	}
	deleted := m.DeleteRead(context.Background(), 7, &mailpb.MailIDsRequest{})
	if deleted.GetRet() != mail.RetOK || len(deleted.GetMailIdList()) != 1 || deleted.GetMailIdList()[0] != plain {
		t.Fatal(deleted.String())
	}
	if got := m.Get(context.Background(), 7, &mailpb.MailGetRequest{MailId: reward}); got.GetRet() != mail.RetOK || got.GetMail().GetClaimed() {
		t.Fatal("unclaimed attachments deleted", got.String())
	}
}
