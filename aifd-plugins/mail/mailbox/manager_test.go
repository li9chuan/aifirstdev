package mailbox

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/generated/mailpb"
	"github.com/li9chuan/aifirstdev/aifd-plugins/mail/store"
	"github.com/jellydator/ttlcache/v3"
)

func TestManagerCachesMailboxAndReturnsClones(t *testing.T) {
	dataStore := &countingStore{MemoryStore: store.NewMemoryStore()}
	manager := newTestManager(t, Options{Store: dataStore})
	ctx := context.Background()
	if err := manager.Add(ctx, 1001, managerTestMail(1)); err != nil {
		t.Fatalf("Add error = %v", err)
	}
	dataStore.loads.Store(0)
	manager.Invalidate(1001)
	if err := manager.SyncShared(ctx, 1001, 0, nil, 100); err != nil {
		t.Fatalf("first SyncShared error = %v", err)
	}
	if err := manager.SyncShared(ctx, 1001, 0, nil, 100); err != nil {
		t.Fatalf("second SyncShared error = %v", err)
	}

	first, err := manager.List(1001, 0, 100)
	if err != nil {
		t.Fatalf("first List error = %v", err)
	}
	first[0].Base.CustomContent.Title = "mutated by caller"
	second, err := manager.List(1001, 0, 100)
	if err != nil {
		t.Fatalf("second List error = %v", err)
	}
	if got := dataStore.loads.Load(); got != 1 {
		t.Fatalf("mailbox loads = %d, want 1", got)
	}
	if got := dataStore.globalLoads.Load(); got != 1 {
		t.Fatalf("shared index loads = %d, want 1", got)
	}
	if got := second[0].GetBase().GetCustomContent().GetTitle(); got != "title" {
		t.Fatalf("cached title = %q, want immutable clone", got)
	}
}

func TestManagerSuppressedLoaderCoalescesConcurrentMisses(t *testing.T) {
	dataStore := &countingStore{MemoryStore: store.NewMemoryStore(), delay: 50 * time.Millisecond}
	manager := newTestManager(t, Options{Store: dataStore})

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := manager.List(1001, 0, 100)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("List error = %v", err)
		}
	}
	if got := dataStore.loads.Load(); got != 1 {
		t.Fatalf("concurrent mailbox loads = %d, want 1", got)
	}
}

func TestManagerReloadsAfterSlidingTTLExpires(t *testing.T) {
	dataStore := &countingStore{MemoryStore: store.NewMemoryStore()}
	manager := newTestManager(t, Options{Store: dataStore, CacheTTL: 60 * time.Millisecond})
	if _, err := manager.List(1001, 0, 100); err != nil {
		t.Fatalf("first List error = %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	if _, err := manager.List(1001, 0, 100); err != nil {
		t.Fatalf("touch List error = %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	if _, err := manager.List(1001, 0, 100); err != nil {
		t.Fatalf("sliding List error = %v", err)
	}
	if got := dataStore.loads.Load(); got != 1 {
		t.Fatalf("mailbox loads after touch = %d, want 1", got)
	}
	time.Sleep(80 * time.Millisecond)
	if _, err := manager.List(1001, 0, 100); err != nil {
		t.Fatalf("expired List error = %v", err)
	}
	if got := dataStore.loads.Load(); got != 2 {
		t.Fatalf("mailbox loads = %d, want 2 after ttl", got)
	}
}

func TestManagerMailboxLimitIsCallerControlled(t *testing.T) {
	dataStore := store.NewMemoryStore()
	manager := newTestManager(t, Options{Store: dataStore, MailboxLimit: 2})
	ctx := context.Background()
	for mailID := int64(1); mailID <= 3; mailID++ {
		if err := manager.Add(ctx, 1001, managerTestMail(mailID)); err != nil {
			t.Fatalf("Add %d error = %v", mailID, err)
		}
	}
	list, err := manager.List(1001, 0, 100)
	if err != nil {
		t.Fatalf("List error = %v", err)
	}
	if len(list) != 2 || list[0].GetMailId() != 2 || list[1].GetMailId() != 3 {
		t.Fatalf("mail ids = %v, want [2 3]", testMailIDs(list))
	}
	box, err := dataStore.LoadPersonalMailbox(ctx, 1001)
	if err != nil || len(box.Mails) != 2 {
		t.Fatalf("durable mailbox = %+v err=%v, want two mails", box, err)
	}

	large, err := NewManager(Options{
		Store:         store.NewMemoryStore(),
		MailboxLimit:  1_000_000,
		CacheTTL:      ttlcache.NoTTL,
		CacheCapacity: -1,
	})
	if err != nil {
		t.Fatalf("NewManager with caller-controlled values error = %v", err)
	}
	large.Close()
}

func TestManagerConcurrentMutationsKeepEveryMail(t *testing.T) {
	manager := newTestManager(t, Options{Store: store.NewMemoryStore(), MailboxLimit: 64})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for mailID := int64(1); mailID <= 32; mailID++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			errs <- manager.Add(ctx, 1001, managerTestMail(id))
		}(mailID)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Add error = %v", err)
		}
	}
	list, err := manager.List(1001, 0, 64)
	if err != nil {
		t.Fatalf("List error = %v", err)
	}
	if len(list) != 32 {
		t.Fatalf("mail count = %d, want 32", len(list))
	}
}

func TestManagerFailedPersistenceDoesNotMutateCache(t *testing.T) {
	dataStore := &failingApplyStore{MemoryStore: store.NewMemoryStore()}
	manager := newTestManager(t, Options{Store: dataStore})
	wantErr := errors.New("storage unavailable")
	dataStore.err.Store(&wantErr)
	if err := manager.Add(context.Background(), 1001, managerTestMail(1)); !errors.Is(err, wantErr) {
		t.Fatalf("Add error = %v, want storage failure", err)
	}
	list, err := manager.List(1001, 0, 100)
	if err != nil {
		t.Fatalf("List error = %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("cached mails = %v, want empty after failed persistence", testMailIDs(list))
	}
}

func TestManagerRejectsInvalidNegativeOptions(t *testing.T) {
	tests := []Options{
		{Store: store.NewMemoryStore(), MailboxLimit: -1},
		{Store: store.NewMemoryStore(), CacheTTL: -2 * time.Second},
		{Store: store.NewMemoryStore(), CacheCapacity: -2},
	}
	for _, opts := range tests {
		if _, err := NewManager(opts); err == nil {
			t.Fatalf("NewManager(%+v) error = nil, want validation error", opts)
		}
	}
}

type countingStore struct {
	*store.MemoryStore
	loads       atomic.Int64
	globalLoads atomic.Int64
	delay       time.Duration
}

type failingApplyStore struct {
	*store.MemoryStore
	err atomic.Pointer[error]
}

func (s *failingApplyStore) ApplyPersonalMailbox(ctx context.Context, rootID int64, version uint64, change store.PersonalMailboxChange) (uint64, error) {
	if value := s.err.Swap(nil); value != nil {
		return version, *value
	}
	return s.MemoryStore.ApplyPersonalMailbox(ctx, rootID, version, change)
}

func (s *countingStore) ListGlobalAfter(ctx context.Context, afterMailID int64, realmID int32, limit int) ([]*mailpb.MailData, error) {
	s.globalLoads.Add(1)
	return s.MemoryStore.ListGlobalAfter(ctx, afterMailID, realmID, limit)
}

func (s *countingStore) LoadPersonalMailbox(ctx context.Context, rootID int64) (*store.PersonalMailbox, error) {
	s.loads.Add(1)
	if s.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(s.delay):
		}
	}
	return s.MemoryStore.LoadPersonalMailbox(ctx, rootID)
}

func newTestManager(t *testing.T, opts Options) *Manager {
	t.Helper()
	manager, err := NewManager(opts)
	if err != nil {
		t.Fatalf("NewManager error = %v", err)
	}
	t.Cleanup(manager.Close)
	return manager
}

func managerTestMail(mailID int64) *mailpb.MailData {
	return &mailpb.MailData{
		MailId:       mailID,
		Type:         mailpb.MailType_MAIL_TYPE_PERSONAL,
		Base:         &mailpb.MailBase{CustomContent: &mailpb.MailCustomContent{Title: "title", Body: "body"}},
		SendTimeUnix: time.Now().Unix(),
		EndTimeUnix:  time.Now().Add(time.Hour).Unix(),
	}
}

func testMailIDs(list []*mailpb.MailData) []int64 {
	out := make([]int64, 0, len(list))
	for _, data := range list {
		out = append(out, data.GetMailId())
	}
	return out
}
