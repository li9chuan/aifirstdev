package rdb

import (
	"context"
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestMemoryHashTTLAppliesToHashKey(t *testing.T) {
	ctx := context.Background()
	db := newDelayedMemoryDB()
	installDefaultDB(t, db)
	store := db.store.(*memoryStore)
	first := &testHashState{}
	if err := first.Bind("rank", "uid:1", wrapperspb.String("one"), WithTTL(time.Hour)); err != nil {
		t.Fatalf("Bind first error = %v", err)
	}
	if err := first.Save(ctx); err != nil {
		t.Fatalf("Save first error = %v", err)
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("Flush first error = %v", err)
	}
	before := store.hashes["rank"].expiresAt
	if before.IsZero() {
		t.Fatal("initial hash expiresAt is zero")
	}

	second := &testHashState{}
	if err := second.Bind("rank", "uid:2", wrapperspb.String("two")); err != nil {
		t.Fatalf("Bind second error = %v", err)
	}
	if err := second.Save(ctx); err != nil {
		t.Fatalf("Save second error = %v", err)
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("Flush second error = %v", err)
	}
	after := store.hashes["rank"].expiresAt
	if !after.Equal(before) {
		t.Fatalf("hash expiresAt changed from %v to %v", before, after)
	}
	keys, err := store.HKeys(ctx, "rank")
	if err != nil {
		t.Fatalf("HKeys error = %v", err)
	}
	if want := []string{"uid:1", "uid:2"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("HKeys = %v, want %v", keys, want)
	}
}

func TestMemoryHashExpirationRemovesAllFields(t *testing.T) {
	ctx := context.Background()
	db := newDelayedMemoryDB()
	installDefaultDB(t, db)
	store := db.store.(*memoryStore)
	first := &testHashState{}
	if err := first.Bind("rank", "uid:1", wrapperspb.String("one"), WithTTL(time.Hour)); err != nil {
		t.Fatalf("Bind first error = %v", err)
	}
	if err := first.Save(ctx); err != nil {
		t.Fatalf("Save first error = %v", err)
	}
	second := &testHashState{}
	if err := second.Bind("rank", "uid:2", wrapperspb.String("two")); err != nil {
		t.Fatalf("Bind second error = %v", err)
	}
	if err := second.Save(ctx); err != nil {
		t.Fatalf("Save second error = %v", err)
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	hash := store.hashes["rank"]
	hash.expiresAt = time.Now().Add(-time.Second)
	store.hashes["rank"] = hash

	keys, err := store.HKeys(ctx, "rank")
	if err != nil {
		t.Fatalf("HKeys error = %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("HKeys = %v, want empty after hash expiration", keys)
	}
	if _, exists := store.hashes["rank"]; exists {
		t.Fatal("expired hash key still exists")
	}
}

func TestWaitListMergesFinalIntentByEntity(t *testing.T) {
	wait := newWaitList()
	wait.PushBack(setSaveIntent("a", 0, nil))
	wait.PushBack(setSaveIntent("a", 0, nil))
	wait.PushBack(setSaveIntent("b", 0, nil))
	if wait.Len() != 2 {
		t.Fatalf("Len = %d, want 2", wait.Len())
	}
	first, ok := wait.PopFront()
	if !ok || first.entityKey() != (entityKey{kind: intentKindSet, key: "a"}) || first.intent != intentSave {
		t.Fatalf("first op = %+v ok=%v, want set a save", first, ok)
	}
	second, ok := wait.PopFront()
	if !ok || second.entityKey() != (entityKey{kind: intentKindSet, key: "b"}) || second.intent != intentSave {
		t.Fatalf("second op = %+v ok=%v, want set b save", second, ok)
	}
}

func TestWaitListDeleteAndSaveOverrideSameEntity(t *testing.T) {
	wait := newWaitList()
	wait.PushBack(setSaveIntent("a", 0, nil))
	wait.PushBack(setDeleteIntent("a"))
	if wait.Len() != 1 {
		t.Fatalf("Len after save delete = %d, want 1", wait.Len())
	}
	op, ok := wait.PopFront()
	if !ok || op.intent != intentDelete {
		t.Fatalf("op after save delete = %+v ok=%v, want delete", op, ok)
	}

	wait.PushBack(setDeleteIntent("a"))
	wait.PushBack(setSaveIntent("a", 0, nil))
	if wait.Len() != 1 {
		t.Fatalf("Len after delete save = %d, want 1", wait.Len())
	}
	op, ok = wait.PopFront()
	if !ok || op.intent != intentSave {
		t.Fatalf("op after delete save = %+v ok=%v, want save", op, ok)
	}
}

func TestWaitListSaveDeleteSaveKeepsFinalIntent(t *testing.T) {
	wait := newWaitList()
	wait.PushBack(setSaveIntent("a", 0, nil))
	wait.PushBack(setDeleteIntent("a"))
	wait.PushBack(setSaveIntent("a", 0, nil))
	if wait.Len() != 1 {
		t.Fatalf("Len after save delete save = %d, want 1", wait.Len())
	}
	op, ok := wait.PopFront()
	if !ok || op.entityKey() != (entityKey{kind: intentKindSet, key: "a"}) || op.intent != intentSave {
		t.Fatalf("op after save delete save = %+v ok=%v, want final save", op, ok)
	}
}

func TestWaitListPopFrontReadyUsesFirstQueuedTime(t *testing.T) {
	wait := newWaitList()
	wait.PushBack(hashSaveIntent("rank", "uid:1", 0, nil))
	entityKey := entityKey{kind: intentKindHash, key: "rank", field: "uid:1"}
	firstQueued := time.Now().Add(-2 * time.Second)
	entry := wait.index[entityKey].Value.(waitEntry)
	entry.firstQueued = firstQueued
	wait.index[entityKey].Value = entry

	wait.PushBack(hashDeleteIntent("rank", "uid:1"))
	entry = wait.index[entityKey].Value.(waitEntry)
	if !entry.firstQueued.Equal(firstQueued) {
		t.Fatalf("firstQueued = %v, want %v", entry.firstQueued, firstQueued)
	}
	if op, ok := wait.PopFrontReady(firstQueued.Add(time.Second), 2*time.Second); ok {
		t.Fatalf("PopFrontReady before min stay = %+v ok=%v, want not ready", op, ok)
	}
	op, ok := wait.PopFrontReady(firstQueued.Add(2*time.Second), 2*time.Second)
	if !ok || op.intent != intentDelete {
		t.Fatalf("PopFrontReady after min stay = %+v ok=%v, want final delete", op, ok)
	}
}

func TestWaitListDoesNotRejectDistinctOpsBySize(t *testing.T) {
	wait := newWaitList()
	for i := 0; i < 1024; i++ {
		wait.PushBack(setSaveIntent(string(rune(i)), 0, nil))
	}
	if wait.Len() != 1024 {
		t.Fatalf("Len = %d, want 1024", wait.Len())
	}
}
