package user

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestStoreRequiresExplicitCreateAndRestoresDocument(t *testing.T) {
	ctx := context.Background()
	setupMemoryDB(t)
	store := newInt64Store(t)
	if _, err := store.Load(ctx, 1001); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing load: %v", err)
	}
	if err := rdb.FlushDefaultDB(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(ctx, 1001); !errors.Is(err, ErrNotFound) {
		t.Fatalf("load created a document: %v", err)
	}
	owner, err := store.Create(ctx, 1001)
	if err != nil {
		t.Fatal(err)
	}
	if owner.UID() != 1001 {
		t.Fatalf("UID = %d", owner.UID())
	}
	if owner.LoadedFromStore() {
		t.Fatal("explicit creation was reported as an existing stored document")
	}
	if err := owner.Mutate(ctx, func(db *wrapperspb.Int64Value) error { db.Value = 77; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := rdb.FlushDefaultDB(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, 1001); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate create: %v", err)
	}
	restored, err := store.Load(ctx, 1001)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.Snapshot().GetValue(); got != 77 {
		t.Fatalf("restored = %d, want 77", got)
	}
	if !restored.LoadedFromStore() {
		t.Fatal("existing primary document was reported as a missing optional block")
	}
}

func TestMutateFailureDoesNotLeakIntoMemoryOrPendingSave(t *testing.T) {
	ctx := context.Background()
	setupMemoryDB(t)
	store := newInt64Store(t)
	owner, err := store.Create(ctx, 1002)
	if err != nil {
		t.Fatal(err)
	}
	rejected := errors.New("business validation failed")
	if err := owner.Mutate(ctx, func(db *wrapperspb.Int64Value) error { db.Value = 99; return rejected }); !errors.Is(err, rejected) {
		t.Fatalf("callback error: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	if err := owner.Mutate(cancelled, func(db *wrapperspb.Int64Value) error { db.Value = 88; cancel(); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled mutation: %v", err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("callback did not panic")
			}
		}()
		_ = owner.Mutate(ctx, func(db *wrapperspb.Int64Value) error { db.Value = 66; panic("reject") })
	}()
	if got := owner.Snapshot().GetValue(); got != 1002 {
		t.Fatalf("published value = %d", got)
	}
	if err := rdb.FlushDefaultDB(ctx); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Load(ctx, 1002)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.Snapshot().GetValue(); got != 1002 {
		t.Fatalf("pending save persisted rejected mutation: %d", got)
	}
}

func TestMutateRejectedQueueRestoresPreviousDocument(t *testing.T) {
	ctx := context.Background()
	db := rdb.MustNewMemoryDB()
	store := newInt64StoreWithDB(t, db)
	owner, err := store.Create(ctx, 1003)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := owner.Mutate(ctx, func(candidate *wrapperspb.Int64Value) error { candidate.Value = 12; return nil }); err == nil {
		t.Fatal("save accepted after database close")
	}
	if got := owner.Snapshot().GetValue(); got != 1003 {
		t.Fatalf("rejected save left value = %d", got)
	}
}

func TestSnapshotAndReadPanicPreserveOwnership(t *testing.T) {
	ctx := context.Background()
	setupMemoryDB(t)
	owner, err := newInt64Store(t).Create(ctx, 1004)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := owner.Snapshot()
	snapshot.Value = 99
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("read callback did not panic")
			}
		}()
		_ = owner.Read(func(*wrapperspb.Int64Value) { panic("read failed") })
	}()
	if err := owner.Mutate(ctx, func(db *wrapperspb.Int64Value) error { db.Value = 12; return nil }); err != nil {
		t.Fatal(err)
	}
	if got := owner.Snapshot().GetValue(); got != 12 {
		t.Fatalf("value = %d", got)
	}
}

func TestCoordinationLockProtectsAdditionalBusinessState(t *testing.T) {
	ctx := context.Background()
	setupMemoryDB(t)
	owner, err := newInt64Store(t).Create(ctx, 1005)
	if err != nil {
		t.Fatal(err)
	}
	var externalBlock int64 = 1005
	owner.Lock()
	saver := owner.SaverUnsafe()
	saver.Lock()
	saver.DbProto.Value = 42
	externalBlock = 42
	if err := saver.SaveUnsafe(ctx); err != nil {
		t.Fatal(err)
	}
	saver.Unlock()
	owner.Unlock()
	owner.RLock()
	saver.RLock()
	if saver.DbProto.GetValue() != externalBlock {
		t.Fatal("cross-block state is inconsistent")
	}
	saver.RUnlock()
	owner.RUnlock()
	if err := owner.Mutate(ctx, func(db *wrapperspb.Int64Value) error { db.Value++; return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestStoresKeepExplicitDatabaseIdentity(t *testing.T) {
	ctx := context.Background()
	firstDB := rdb.MustNewMemoryDB()
	defer firstDB.Close(ctx)
	secondDB := rdb.MustNewMemoryDB()
	defer secondDB.Close(ctx)
	firstStore, secondStore := newInt64StoreWithDB(t, firstDB), newInt64StoreWithDB(t, secondDB)
	first, err := firstStore.Create(ctx, 1006)
	if err != nil {
		t.Fatal(err)
	}
	second, err := secondStore.Create(ctx, 1006)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Mutate(ctx, func(db *wrapperspb.Int64Value) error { db.Value = 11; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := second.Mutate(ctx, func(db *wrapperspb.Int64Value) error { db.Value = 22; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := firstDB.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := secondDB.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	restoredFirst, err := firstStore.Load(ctx, 1006)
	if err != nil {
		t.Fatal(err)
	}
	restoredSecond, err := secondStore.Load(ctx, 1006)
	if err != nil {
		t.Fatal(err)
	}
	if restoredFirst.Snapshot().GetValue() != 11 || restoredSecond.Snapshot().GetValue() != 22 {
		t.Fatal("stores crossed database identities")
	}
}

func setupMemoryDB(t *testing.T) {
	t.Helper()
	rdb.SetDefaultDB(rdb.MustNewMemoryDB(rdb.WithSaveDelay(time.Millisecond, time.Millisecond)))
	t.Cleanup(func() { _ = rdb.CloseDefaultDB(context.Background()) })
}

func TestExistingIdentityMayHaveNoPrimaryPBWithoutCreatingEmptyDocument(t *testing.T) {
	ctx := context.Background()
	setupMemoryDB(t)
	store, err := NewStore(StoreOptions[*wrapperspb.Int64Value]{
		Key:    func(uid int64) string { return "optional:" + strconv.FormatInt(uid, 10) },
		New:    func(uid int64) *wrapperspb.Int64Value { return wrapperspb.Int64(uid) },
		Exists: func(_ context.Context, uid int64) (bool, error) { return uid == 42, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.Load(ctx, 42)
	if err != nil || owner.UID() != 42 {
		t.Fatalf("known identity: %v", err)
	}
	if owner.LoadedFromStore() {
		t.Fatal("identity existence was confused with primary document presence")
	}
	if _, err := store.Load(ctx, 43); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown identity: %v", err)
	}
	if _, err := store.Create(ctx, 42); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("existing identity create: %v", err)
	}
	if err := rdb.FlushDefaultDB(ctx); err != nil {
		t.Fatal(err)
	}
	strict, _ := NewStore(StoreOptions[*wrapperspb.Int64Value]{Key: store.opts.Key, New: store.opts.New})
	if _, err := strict.Load(ctx, 42); !errors.Is(err, ErrNotFound) {
		t.Fatalf("read wrote empty document: %v", err)
	}
}

func newInt64Store(t *testing.T) *Store[*wrapperspb.Int64Value] { return newInt64StoreWithDB(t, nil) }

func newInt64StoreWithDB(t *testing.T, db *rdb.DB) *Store[*wrapperspb.Int64Value] {
	t.Helper()
	store, err := NewStore(StoreOptions[*wrapperspb.Int64Value]{DB: db, Key: func(uid int64) string { return "root:" + strconv.FormatInt(uid, 10) }, New: func(uid int64) *wrapperspb.Int64Value { return wrapperspb.Int64(uid) }})
	if err != nil {
		t.Fatal(err)
	}
	return store
}
