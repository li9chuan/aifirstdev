package root

import (
	"context"
	"strconv"
	"testing"
	"time"

	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestStoreLoadOrNewCreatesAndRestoresOwner(t *testing.T) {
	ctx := context.Background()
	setupMemoryDB(t)
	store := newInt64Store(t)

	owner, isNew, err := store.LoadOrNew(ctx, 1001)
	if err != nil {
		t.Fatalf("LoadOrNew new error = %v", err)
	}
	if !isNew {
		t.Fatal("isNew = false, want true")
	}
	if err := owner.Mutate(ctx, func(db *wrapperspb.Int64Value) error {
		db.Value = 77
		return nil
	}); err != nil {
		t.Fatalf("Mutate error = %v", err)
	}
	if err := rdb.FlushDefaultDB(ctx); err != nil {
		t.Fatalf("FlushDefaultDB error = %v", err)
	}

	restored, isNew, err := store.LoadOrNew(ctx, 1001)
	if err != nil {
		t.Fatalf("LoadOrNew restored error = %v", err)
	}
	if isNew {
		t.Fatal("isNew = true, want false")
	}
	snapshot := restored.Snapshot()
	if snapshot.GetValue() != 77 {
		t.Fatalf("snapshot value = %d, want 77", snapshot.GetValue())
	}
}

func TestSnapshotReturnsDeepCopy(t *testing.T) {
	ctx := context.Background()
	setupMemoryDB(t)
	store := newInt64Store(t)
	owner, _, err := store.LoadOrNew(ctx, 1002)
	if err != nil {
		t.Fatalf("LoadOrNew error = %v", err)
	}
	if err := owner.Mutate(ctx, func(db *wrapperspb.Int64Value) error {
		db.Value = 12
		return nil
	}); err != nil {
		t.Fatalf("Mutate error = %v", err)
	}

	snapshot := owner.Snapshot()
	snapshot.Value = 99
	var value int64
	if err := owner.Read(func(db *wrapperspb.Int64Value) {
		value = db.GetValue()
	}); err != nil {
		t.Fatalf("Read error = %v", err)
	}
	if value != 12 {
		t.Fatalf("value = %d, want 12", value)
	}
}

func TestStoreUsesExplicitDB(t *testing.T) {
	ctx := context.Background()
	firstDB := rdb.MustNewMemoryDB(rdb.WithSaveDelay(time.Millisecond, time.Millisecond))
	defer firstDB.Close(ctx)
	secondDB := rdb.MustNewMemoryDB(rdb.WithSaveDelay(time.Millisecond, time.Millisecond))
	defer secondDB.Close(ctx)

	firstStore := newInt64StoreWithDB(t, firstDB)
	secondStore := newInt64StoreWithDB(t, secondDB)
	first, _, err := firstStore.LoadOrNew(ctx, 1001)
	if err != nil {
		t.Fatalf("first LoadOrNew error = %v", err)
	}
	second, _, err := secondStore.LoadOrNew(ctx, 1001)
	if err != nil {
		t.Fatalf("second LoadOrNew error = %v", err)
	}
	if err := first.Mutate(ctx, func(db *wrapperspb.Int64Value) error {
		db.Value = 11
		return nil
	}); err != nil {
		t.Fatalf("first Mutate error = %v", err)
	}
	if err := second.Mutate(ctx, func(db *wrapperspb.Int64Value) error {
		db.Value = 22
		return nil
	}); err != nil {
		t.Fatalf("second Mutate error = %v", err)
	}
	if err := firstDB.Flush(ctx); err != nil {
		t.Fatalf("first Flush error = %v", err)
	}
	if err := secondDB.Flush(ctx); err != nil {
		t.Fatalf("second Flush error = %v", err)
	}

	restoredFirst, isNew, err := firstStore.LoadOrNew(ctx, 1001)
	if err != nil {
		t.Fatalf("restoredFirst LoadOrNew error = %v", err)
	}
	if isNew || restoredFirst.Snapshot().GetValue() != 11 {
		t.Fatalf("restoredFirst isNew=%v value=%d, want existing 11", isNew, restoredFirst.Snapshot().GetValue())
	}
	restoredSecond, isNew, err := secondStore.LoadOrNew(ctx, 1001)
	if err != nil {
		t.Fatalf("restoredSecond LoadOrNew error = %v", err)
	}
	if isNew || restoredSecond.Snapshot().GetValue() != 22 {
		t.Fatalf("restoredSecond isNew=%v value=%d, want existing 22", isNew, restoredSecond.Snapshot().GetValue())
	}
}

func setupMemoryDB(t *testing.T) {
	t.Helper()
	rdb.SetDefaultDB(rdb.MustNewMemoryDB(rdb.WithSaveDelay(time.Millisecond, time.Millisecond)))
	t.Cleanup(func() {
		_ = rdb.CloseDefaultDB(context.Background())
	})
}

func newInt64Store(t *testing.T) *Store[*wrapperspb.Int64Value] {
	return newInt64StoreWithDB(t, nil)
}

func newInt64StoreWithDB(t *testing.T, db *rdb.DB) *Store[*wrapperspb.Int64Value] {
	t.Helper()
	store, err := NewStore(StoreOptions[*wrapperspb.Int64Value]{
		DB: db,
		Key: func(rootID int64) string {
			return "root:" + strconv.FormatInt(rootID, 10)
		},
		New: func(rootID int64) *wrapperspb.Int64Value {
			return wrapperspb.Int64(rootID)
		},
	})
	if err != nil {
		t.Fatalf("NewStore error = %v", err)
	}
	return store
}
