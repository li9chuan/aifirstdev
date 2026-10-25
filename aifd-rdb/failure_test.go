package rdb

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestFailedWriteStaysUnresolvedUntilExplicitFlush(t *testing.T) {
	ctx := context.Background()
	store := newScriptedStore()
	store.err = errors.New("NOPERM injected permission failure")
	store.failSet = 2
	db := newJournalTestDB(store, t.TempDir(), nil)
	defer db.Close(ctx)
	state := &testSetState{}
	if err := state.BindDB(db, "state", wrapperspb.String("saved")); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(ctx); err != nil {
		t.Fatal(err)
	}
	db.lastSave = time.Now().Add(-2 * time.Hour)
	db.drainDue(ctx)
	if store.setAttempts != 1 || db.WaitSize() != 1 {
		t.Fatal("worker discarded failed write", store.setAttempts, db.WaitSize())
	}
	for range 3 {
		db.lastSave = time.Now().Add(-2 * time.Hour)
		db.drainDue(ctx)
	}
	if store.setAttempts != 1 {
		t.Fatal("worker automatically retried a non-retryable failure")
	}
	if err := db.Flush(ctx); !errors.Is(err, store.err) || db.WaitSize() != 1 || store.setAttempts != 2 {
		t.Fatal("explicit Flush forgot unresolved failure", err, db.WaitSize())
	}
	if err := db.Flush(ctx); err != nil || db.WaitSize() != 0 || store.setAttempts != 3 {
		t.Fatal("explicit Flush did not resolve repaired write", err, db.WaitSize())
	}
	raw, found, err := store.Get(ctx, "state")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	loaded := &wrapperspb.StringValue{}
	if err := proto.Unmarshal(raw, loaded); err != nil || loaded.GetValue() != "saved" {
		t.Fatal(loaded, err)
	}
}

func TestFailedSaveAndDeleteCanBeSuperseded(t *testing.T) {
	for _, failedDelete := range []bool{false, true} {
		t.Run(map[bool]string{false: "save-to-delete", true: "delete-to-save"}[failedDelete], func(t *testing.T) {
			ctx := context.Background()
			store := newScriptedStore()
			store.err = errors.New("WRONGTYPE injected failure")
			db := newJournalTestDB(store, t.TempDir(), nil)
			defer db.Close(ctx)
			state := &testHashState{}
			if err := state.BindDB(db, "hash", "field", wrapperspb.String("old")); err != nil {
				t.Fatal(err)
			}
			if failedDelete {
				store.failHDelete = 1
				if !state.Delete(ctx) {
					t.Fatal("delete not accepted")
				}
			} else {
				store.failHSet = 1
				if err := state.Save(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Flush(ctx); !errors.Is(err, store.err) {
				t.Fatal(err)
			}
			if failedDelete {
				replacement := &testHashState{}
				if err := replacement.BindDB(db, "hash", "field", wrapperspb.String("new")); err != nil {
					t.Fatal(err)
				}
				if err := replacement.Save(ctx); err != nil {
					t.Fatal(err)
				}
			} else if !state.Delete(ctx) {
				t.Fatal("replacement delete not accepted")
			}
			if err := db.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			if store.hsetAttempts != 1 || store.hdeleteAttempts != 1 || db.WaitSize() != 0 {
				t.Fatal("superseded failure executed again", store.hsetAttempts, store.hdeleteAttempts, db.WaitSize())
			}
			raw, found, err := store.HGet(ctx, "hash", "field")
			if err != nil || found != failedDelete {
				t.Fatal(found, err)
			}
			if found {
				loaded := &wrapperspb.StringValue{}
				if err := proto.Unmarshal(raw, loaded); err != nil || loaded.GetValue() != "new" {
					t.Fatal(loaded, err)
				}
			}
		})
	}
}

func TestLateFailureDoesNotReplaceNewQueuedIntent(t *testing.T) {
	ctx := context.Background()
	store := newScriptedStore()
	store.err = errors.New("NOPERM injected failure")
	store.failSet = 1
	var state testSetState
	db := newJournalTestDB(store, t.TempDir(), func(_ context.Context, _ ErrorEvent) {
		state.Lock()
		state.DbProto.Value = "new"
		state.Unlock()
		if err := state.Save(ctx); err != nil {
			t.Error(err)
		}
	})
	defer db.Close(ctx)
	if err := state.BindDB(db, "state", wrapperspb.String("old")); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Flush(ctx); err != nil || db.WaitSize() != 0 {
		t.Fatal("old failure blocked newer intent", err, db.WaitSize())
	}
	raw, found, err := store.Get(ctx, "state")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	loaded := &wrapperspb.StringValue{}
	if err := proto.Unmarshal(raw, loaded); err != nil || loaded.GetValue() != "new" {
		t.Fatal(loaded, err)
	}
}

func TestCloseKeepsNonRetryableFailureAndDoesNotJournalIt(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := newScriptedStore()
	store.err = errors.New("NOPERM injected failure")
	store.failSet = 1
	db := newJournalTestDB(store, dir, nil)
	var state testSetState
	if err := state.BindDB(db, "state", wrapperspb.String("saved")); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := db.Close(ctx); !errors.Is(err, store.err) || db.WaitSize() != 1 {
			t.Fatal("Close hid unresolved failure", err, db.WaitSize())
		}
	}
	if store.setAttempts != 1 || len(findJournalFiles(t, dir, ".jsonl")) != 0 {
		t.Fatal("Close retried or journaled non-retryable failure")
	}
	if err := db.Flush(ctx); err != nil || db.WaitSize() != 0 {
		t.Fatal("explicit recovery after Close failed", err, db.WaitSize())
	}
	if err := db.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestFailedWriteMayBecomeRetryableDuringExplicitFlush(t *testing.T) {
	ctx := context.Background()
	store := newScriptedStore()
	store.err = errors.New("NOPERM injected failure")
	store.failSet = 2
	db := newJournalTestDB(store, t.TempDir(), nil)
	defer db.Close(ctx)
	var state testSetState
	if err := state.BindDB(db, "state", wrapperspb.String("saved")); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Flush(ctx); !errors.Is(err, store.err) {
		t.Fatal(err)
	}
	store.err = errRetryableWriteFailure
	if err := db.Flush(ctx); !errors.Is(err, store.err) || db.WaitSize() != 1 {
		t.Fatal("retryable failure lost intent", err, db.WaitSize())
	}
	if err := db.Flush(ctx); err != nil || db.WaitSize() != 0 {
		t.Fatal(err, db.WaitSize())
	}
}

func TestExplicitFailedFlushPreservesOtherRetrySlot(t *testing.T) {
	ctx := context.Background()
	store := newScriptedStore()
	store.err = errors.New("NOPERM injected failure")
	store.failSet = 1
	db := newJournalTestDB(store, t.TempDir(), nil)
	defer db.Close(ctx)
	for _, key := range []string{"failed", "retrying"} {
		var state testSetState
		if err := state.BindDB(db, key, wrapperspb.String(key)); err != nil {
			t.Fatal(err)
		}
		if err := state.Save(ctx); err != nil {
			t.Fatal(err)
		}
		if key == "failed" {
			if err := db.Flush(ctx); !errors.Is(err, store.err) {
				t.Fatal(err)
			}
			store.err = errRetryableWriteFailure
			store.failSet = 2
		} else {
			db.lastSave = time.Now().Add(-2 * time.Hour)
			db.drainDue(ctx)
		}
	}
	if db.WaitSize() != 2 {
		t.Fatal("setup did not retain both failures", db.WaitSize())
	}
	if err := db.Flush(ctx); !errors.Is(err, store.err) || db.WaitSize() != 2 {
		t.Fatal("explicit failed flush displaced another retry slot", err, db.WaitSize())
	}
	if err := db.Flush(ctx); err != nil || db.WaitSize() != 0 {
		t.Fatal(err, db.WaitSize())
	}
	for _, key := range []string{"failed", "retrying"} {
		if _, found, err := store.Get(ctx, key); err != nil || !found {
			t.Fatal("lost final intent", key, found, err)
		}
	}
}

func TestMarshalFailureRetainsPayloadForExplicitRecovery(t *testing.T) {
	ctx := context.Background()
	db := newJournalTestDB(newMemoryStore(), t.TempDir(), nil)
	defer db.Close(ctx)
	var state testSetState
	if err := state.BindDB(db, "state", wrapperspb.String(string([]byte{0xff}))); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Flush(ctx); err == nil || db.WaitSize() != 1 {
		t.Fatal("invalid UTF-8 save was not retained", err, db.WaitSize())
	}
	state.Lock()
	state.DbProto.Value = "repaired"
	state.Unlock()
	if err := db.Flush(ctx); err != nil || db.WaitSize() != 0 {
		t.Fatal("repaired payload was not saved", err, db.WaitSize())
	}
	raw, found, err := db.store.Get(ctx, "state")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	loaded := &wrapperspb.StringValue{}
	if err := proto.Unmarshal(raw, loaded); err != nil || loaded.GetValue() != "repaired" {
		t.Fatal(loaded, err)
	}
}
