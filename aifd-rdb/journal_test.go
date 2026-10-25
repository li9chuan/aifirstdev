package rdb

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var errRetryableWriteFailure = errors.New("dial tcp injected retryable failure")

type scriptedStore struct {
	base *memoryStore
	err  error

	failSet     int
	failDelete  int
	failHSet    int
	failHDelete int
	failExpire  int

	setAttempts     int
	deleteAttempts  int
	hsetAttempts    int
	hdeleteAttempts int
	expireAttempts  int
}

func newScriptedStore() *scriptedStore {
	return &scriptedStore{base: newMemoryStore(), err: errRetryableWriteFailure}
}

func (s *scriptedStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	return s.base.Get(ctx, key)
}

func (s *scriptedStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	s.setAttempts++
	if s.failSet > 0 {
		s.failSet--
		return s.err
	}
	return s.base.Set(ctx, key, value, ttl)
}

func (s *scriptedStore) Delete(ctx context.Context, key string) error {
	s.deleteAttempts++
	if s.failDelete > 0 {
		s.failDelete--
		return s.err
	}
	return s.base.Delete(ctx, key)
}

func (s *scriptedStore) Expire(ctx context.Context, key string, ttl time.Duration) error {
	s.expireAttempts++
	if s.failExpire > 0 {
		s.failExpire--
		return s.err
	}
	return s.base.Expire(ctx, key, ttl)
}

func (s *scriptedStore) HGet(ctx context.Context, key, field string) ([]byte, bool, error) {
	return s.base.HGet(ctx, key, field)
}

func (s *scriptedStore) HSet(ctx context.Context, key, field string, value []byte) error {
	s.hsetAttempts++
	if s.failHSet > 0 {
		s.failHSet--
		return s.err
	}
	return s.base.HSet(ctx, key, field, value)
}

func (s *scriptedStore) HDelete(ctx context.Context, key, field string) error {
	s.hdeleteAttempts++
	if s.failHDelete > 0 {
		s.failHDelete--
		return s.err
	}
	return s.base.HDelete(ctx, key, field)
}

func (s *scriptedStore) HKeys(ctx context.Context, key string) ([]string, error) {
	return s.base.HKeys(ctx, key)
}

func (s *scriptedStore) HGetAll(ctx context.Context, key string) (map[string][]byte, error) {
	return s.base.HGetAll(ctx, key)
}
func (s *scriptedStore) Persist(ctx context.Context, key string) error {
	return s.base.Persist(ctx, key)
}

func newJournalTestDB(store store, dir string, handler ErrorHandler) *DB {
	return newDB(store, options{
		minSaveDelay:   time.Hour,
		maxSaveDelay:   time.Hour,
		minRetryDelay:  time.Millisecond,
		maxRetryDelay:  time.Millisecond,
		failureFileDir: dir,
		errorHandler:   handler,
	})
}

func TestFlushRetryableFailureKeepsQueueAndRetriesLater(t *testing.T) {
	ctx := context.Background()
	store := newScriptedStore()
	store.failSet = 1
	var events []ErrorEvent
	db := newJournalTestDB(store, t.TempDir(), func(_ context.Context, event ErrorEvent) {
		events = append(events, event)
	})
	installDefaultDB(t, db)
	state := &testSetState{}
	if err := state.Bind("player:retry", wrapperspb.String("saved")); err != nil {
		t.Fatalf("Bind error = %v", err)
	}
	if err := state.Save(ctx); err != nil {
		t.Fatalf("Save error = %v", err)
	}
	if err := db.Flush(ctx); !errors.Is(err, errRetryableWriteFailure) {
		t.Fatalf("Flush retry error = %v, want %v", err, errRetryableWriteFailure)
	}
	if db.WaitSize() != 1 {
		t.Fatalf("WaitSize after retryable Flush = %d, want 1", db.WaitSize())
	}
	if len(events) != 1 || !events[0].Retryable || events[0].Stage != "flush" {
		t.Fatalf("events = %+v, want one retryable flush event", events)
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("second Flush error = %v", err)
	}
	if db.WaitSize() != 0 {
		t.Fatalf("WaitSize after second Flush = %d, want 0", db.WaitSize())
	}
}

func TestRetryingSaveCanBeOverriddenByDelete(t *testing.T) {
	ctx := context.Background()
	store := newScriptedStore()
	store.failSet = 1
	db := newJournalTestDB(store, t.TempDir(), nil)
	installDefaultDB(t, db)
	state := &testSetState{}
	if err := state.Bind("player:override-delete", wrapperspb.String("saved")); err != nil {
		t.Fatalf("Bind error = %v", err)
	}
	if err := state.Save(ctx); err != nil {
		t.Fatalf("Save error = %v", err)
	}
	if err := db.Flush(ctx); !errors.Is(err, errRetryableWriteFailure) {
		t.Fatalf("Flush error = %v, want retryable", err)
	}
	if !state.Delete(ctx) {
		t.Fatal("Delete = false, want true")
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("Flush delete error = %v", err)
	}
	if store.setAttempts != 1 || store.deleteAttempts != 1 {
		t.Fatalf("attempts set=%d delete=%d, want 1 failed set and 1 delete", store.setAttempts, store.deleteAttempts)
	}
}

func TestRetryingDeleteCanBeOverriddenByNewSave(t *testing.T) {
	ctx := context.Background()
	store := newScriptedStore()
	store.failDelete = 1
	db := newJournalTestDB(store, t.TempDir(), nil)
	installDefaultDB(t, db)
	deleted := &testSetState{}
	if err := deleted.Bind("player:override-save", wrapperspb.String("old")); err != nil {
		t.Fatalf("deleted Bind error = %v", err)
	}
	if !deleted.Delete(ctx) {
		t.Fatal("Delete = false, want true")
	}
	if err := db.Flush(ctx); !errors.Is(err, errRetryableWriteFailure) {
		t.Fatalf("Flush delete error = %v, want retryable", err)
	}

	saved := &testSetState{}
	if err := saved.Bind("player:override-save", wrapperspb.String("new")); err != nil {
		t.Fatalf("saved Bind error = %v", err)
	}
	if err := saved.Save(ctx); err != nil {
		t.Fatalf("Save error = %v", err)
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("Flush save error = %v", err)
	}
	loaded := &wrapperspb.StringValue{}
	raw, ok, err := store.Get(ctx, "player:override-save")
	if err != nil || !ok {
		t.Fatalf("Get ok=%v err=%v, want true nil", ok, err)
	}
	if err := proto.Unmarshal(raw, loaded); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}
	if loaded.GetValue() != "new" {
		t.Fatalf("loaded value = %q, want new", loaded.GetValue())
	}
}

func TestCloseWritesJournalAndReplayAppliesIt(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := newScriptedStore()
	store.failSet = 1
	db := newJournalTestDB(store, dir, nil)
	installDefaultDB(t, db)
	state := &testSetState{}
	if err := state.Bind("player:journal", wrapperspb.String("saved")); err != nil {
		t.Fatalf("Bind error = %v", err)
	}
	if err := state.Save(ctx); err != nil {
		t.Fatalf("Save error = %v", err)
	}
	if err := db.Close(ctx); !errors.Is(err, errRetryableWriteFailure) {
		t.Fatalf("Close error = %v, want retryable", err)
	}
	records := readOnlyJournalRecords(t, dir, ".jsonl")
	if len(records) != 1 {
		t.Fatalf("journal records = %d, want 1", len(records))
	}
	if records[0].Source != journalSourceClose || records[0].Kind != intentKindSet || records[0].Intent != intentSave {
		t.Fatalf("journal record = %+v, want close set save", records[0])
	}
	value, err := base64.StdEncoding.DecodeString(records[0].ValueBase64)
	if err != nil {
		t.Fatalf("DecodeString error = %v", err)
	}
	msg := &wrapperspb.StringValue{}
	if err := proto.Unmarshal(value, msg); err != nil {
		t.Fatalf("Unmarshal journal value error = %v", err)
	}
	if msg.GetValue() != "saved" {
		t.Fatalf("journal value = %q, want saved", msg.GetValue())
	}

	replayStore := newMemoryStore()
	if err := replayStartupJournals(ctx, replayStore, dir); err != nil {
		t.Fatalf("replayStartupJournals error = %v", err)
	}
	raw, ok, err := replayStore.Get(ctx, "player:journal")
	if err != nil || !ok {
		t.Fatalf("replayed Get ok=%v err=%v, want true nil", ok, err)
	}
	if err := proto.Unmarshal(raw, msg); err != nil {
		t.Fatalf("Unmarshal replayed value error = %v", err)
	}
	if msg.GetValue() != "saved" {
		t.Fatalf("replayed value = %q, want saved", msg.GetValue())
	}
	if len(findJournalFiles(t, dir, ".jsonl")) != 0 || len(findJournalFiles(t, dir, ".done")) != 1 {
		t.Fatalf("journal file states jsonl=%v done=%v", findJournalFiles(t, dir, ".jsonl"), findJournalFiles(t, dir, ".done"))
	}
}

func TestReplayAggregatesHashTTLWhenAllHashIntentsAreSaves(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := newScriptedStore()
	store.failHSet = 1
	db := newJournalTestDB(store, dir, nil)
	installDefaultDB(t, db)
	first := &testHashState{}
	if err := first.Bind("rank:journal-ttl", "uid:1", wrapperspb.String("one"), WithTTL(time.Hour)); err != nil {
		t.Fatalf("Bind first error = %v", err)
	}
	if err := first.Save(ctx); err != nil {
		t.Fatalf("Save first error = %v", err)
	}
	second := &testHashState{}
	if err := second.Bind("rank:journal-ttl", "uid:2", wrapperspb.String("two"), WithTTL(2*time.Hour)); err != nil {
		t.Fatalf("Bind second error = %v", err)
	}
	if err := second.Save(ctx); err != nil {
		t.Fatalf("Save second error = %v", err)
	}
	if err := db.Close(ctx); !errors.Is(err, errRetryableWriteFailure) {
		t.Fatalf("Close error = %v, want retryable", err)
	}

	replayStore := newScriptedStore()
	if err := replayStartupJournals(ctx, replayStore, dir); err != nil {
		t.Fatalf("replayStartupJournals error = %v", err)
	}
	if replayStore.expireAttempts != 1 {
		t.Fatalf("expireAttempts = %d, want 1", replayStore.expireAttempts)
	}
	keys, err := replayStore.HKeys(ctx, "rank:journal-ttl")
	if err != nil {
		t.Fatalf("HKeys error = %v", err)
	}
	if want := []string{"uid:1", "uid:2"}; !equalStrings(keys, want) {
		t.Fatalf("HKeys = %v, want %v", keys, want)
	}
}

func TestReplayRejectsStaleReplayingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rdb-close.replaying")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	if err := replayStartupJournals(context.Background(), newMemoryStore(), dir); !errors.Is(err, ErrJournalReplayInProgress) {
		t.Fatalf("replay error = %v, want %v", err, ErrJournalReplayInProgress)
	}
}

func readOnlyJournalRecords(t *testing.T, dir, suffix string) []journalRecord {
	t.Helper()
	paths := findJournalFiles(t, dir, suffix)
	if len(paths) != 1 {
		t.Fatalf("journal files %s = %v, want one", suffix, paths)
	}
	records, err := readJournalRecords(paths[0])
	if err != nil {
		t.Fatalf("readJournalRecords error = %v", err)
	}
	return records
}

func findJournalFiles(t *testing.T, dir, suffix string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir error = %v", err)
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) == suffix {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	return paths
}
