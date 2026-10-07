package rdb

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/wrapperspb"
)

type testSetState struct {
	SetSaver[*wrapperspb.StringValue]
}

type testHashState struct {
	HashSaver[*wrapperspb.StringValue]
}

func installDefaultDB(t *testing.T, db *DB) {
	t.Helper()
	previous := DefaultDB()
	SetDefaultDB(db)
	t.Cleanup(func() {
		_ = db.Close(context.Background())
		SetDefaultDB(previous)
	})
}

func newDelayedMemoryDB() *DB {
	return MustNewMemoryDB(WithSaveDelay(time.Hour, time.Hour))
}

func pendingIntentKeys(wait *waitList) []string {
	keys := make([]string, 0, wait.items.Len())
	for element := wait.items.Front(); element != nil; element = element.Next() {
		op := element.Value.(waitEntry).op
		keys = append(keys, op.entityKey()+":"+op.intent)
	}
	return keys
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

var errInjectedSetFailure = errors.New("injected set failure")

func TestSetSaverBindDBKeepsStoresIndependent(t *testing.T) {
	ctx := context.Background()
	firstDB := MustNewMemoryDB()
	defer firstDB.Close(ctx)
	secondDB := MustNewMemoryDB()
	defer secondDB.Close(ctx)

	var first SetSaver[*wrapperspb.StringValue]
	if err := first.BindDB(firstDB, "same-key", wrapperspb.String("first")); err != nil {
		t.Fatalf("first BindDB error = %v", err)
	}
	if err := first.Save(ctx); err != nil {
		t.Fatalf("first Save error = %v", err)
	}
	if err := firstDB.Flush(ctx); err != nil {
		t.Fatalf("first Flush error = %v", err)
	}

	var second SetSaver[*wrapperspb.StringValue]
	if err := second.BindDB(secondDB, "same-key", wrapperspb.String("second")); err != nil {
		t.Fatalf("second BindDB error = %v", err)
	}
	if err := second.Save(ctx); err != nil {
		t.Fatalf("second Save error = %v", err)
	}
	if err := secondDB.Flush(ctx); err != nil {
		t.Fatalf("second Flush error = %v", err)
	}

	var loadedFirst SetSaver[*wrapperspb.StringValue]
	if err := loadedFirst.BindDB(firstDB, "same-key", wrapperspb.String("")); err != nil {
		t.Fatalf("loadedFirst BindDB error = %v", err)
	}
	if ok, err := loadedFirst.Load(ctx); err != nil || !ok {
		t.Fatalf("loadedFirst Load ok=%v err=%v", ok, err)
	}
	var loadedSecond SetSaver[*wrapperspb.StringValue]
	if err := loadedSecond.BindDB(secondDB, "same-key", wrapperspb.String("")); err != nil {
		t.Fatalf("loadedSecond BindDB error = %v", err)
	}
	if ok, err := loadedSecond.Load(ctx); err != nil || !ok {
		t.Fatalf("loadedSecond Load ok=%v err=%v", ok, err)
	}
	if loadedFirst.DbProto.GetValue() != "first" || loadedSecond.DbProto.GetValue() != "second" {
		t.Fatalf("loaded values = %q/%q, want first/second", loadedFirst.DbProto.GetValue(), loadedSecond.DbProto.GetValue())
	}
}

type failFirstSetStore struct {
	values      map[string][]byte
	setKeys     []string
	failNextSet bool
}

func (s *failFirstSetStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	value, ok := s.values[key]
	return cloneBytes(value), ok, nil
}

func (s *failFirstSetStore) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	s.setKeys = append(s.setKeys, key)
	if s.failNextSet {
		s.failNextSet = false
		return errInjectedSetFailure
	}
	s.values[key] = cloneBytes(value)
	return nil
}

func (s *failFirstSetStore) Delete(_ context.Context, key string) error {
	delete(s.values, key)
	return nil
}

func (s *failFirstSetStore) Expire(context.Context, string, time.Duration) error {
	return nil
}

func (s *failFirstSetStore) HGet(context.Context, string, string) ([]byte, bool, error) {
	return nil, false, nil
}

func (s *failFirstSetStore) HSet(context.Context, string, string, []byte) error {
	return nil
}

func (s *failFirstSetStore) HDelete(context.Context, string, string) error {
	return nil
}

func (s *failFirstSetStore) HKeys(context.Context, string) ([]string, error) {
	return nil, nil
}

func TestSetSaverEnqueuesAndFlushes(t *testing.T) {
	ctx := context.Background()
	db := newDelayedMemoryDB()
	installDefaultDB(t, db)
	state := &testSetState{}
	if err := state.Bind("player:1", wrapperspb.String("saved")); err != nil {
		t.Fatalf("Bind error = %v", err)
	}
	if err := state.Save(ctx); err != nil {
		t.Fatalf("Save error = %v", err)
	}

	loaded := &testSetState{}
	if err := loaded.Bind("player:1", wrapperspb.String("default")); err != nil {
		t.Fatalf("loaded Bind error = %v", err)
	}
	ok, err := loaded.Load(ctx)
	if err != nil {
		t.Fatalf("Load before Flush error = %v", err)
	}
	if ok {
		t.Fatal("Load before Flush ok = true, want false")
	}
	if db.WaitSize() != 1 {
		t.Fatalf("WaitSize = %d, want 1", db.WaitSize())
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("Flush error = %v", err)
	}

	ok, err = loaded.Load(ctx)
	if err != nil || !ok {
		t.Fatalf("Load after Flush ok=%v err=%v, want true nil", ok, err)
	}
	if loaded.DbProto.GetValue() != "saved" {
		t.Fatalf("loaded value = %q, want saved", loaded.DbProto.GetValue())
	}
}

func TestSetSaverMergeUsesLatestProto(t *testing.T) {
	ctx := context.Background()
	db := newDelayedMemoryDB()
	installDefaultDB(t, db)
	state := &testSetState{}
	if err := state.Bind("player:merge", wrapperspb.String("first")); err != nil {
		t.Fatalf("Bind error = %v", err)
	}
	if err := state.Save(ctx); err != nil {
		t.Fatalf("first Save error = %v", err)
	}
	state.DbProto.Value = "second"
	if err := state.Save(ctx); err != nil {
		t.Fatalf("second Save error = %v", err)
	}
	if db.WaitSize() != 1 {
		t.Fatalf("WaitSize = %d, want merged size 1", db.WaitSize())
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("Flush error = %v", err)
	}

	loaded := &testSetState{}
	if err := loaded.Bind("player:merge", &wrapperspb.StringValue{}); err != nil {
		t.Fatalf("loaded Bind error = %v", err)
	}
	ok, err := loaded.Load(ctx)
	if err != nil || !ok {
		t.Fatalf("Load ok=%v err=%v, want true nil", ok, err)
	}
	if loaded.DbProto.GetValue() != "second" {
		t.Fatalf("loaded value = %q, want second", loaded.DbProto.GetValue())
	}
}

func TestSetSaverDeleteOrderAndNoopSave(t *testing.T) {
	ctx := context.Background()
	db := newDelayedMemoryDB()
	installDefaultDB(t, db)
	state := &testSetState{}
	if err := state.Bind("player:delete", wrapperspb.String("saved")); err != nil {
		t.Fatalf("Bind error = %v", err)
	}
	if err := state.Save(ctx); err != nil {
		t.Fatalf("Save error = %v", err)
	}
	if !state.Delete(ctx) {
		t.Fatal("Delete = false, want true")
	}
	if state.Delete(ctx) {
		t.Fatal("second Delete = true, want false")
	}
	if db.WaitSize() != 1 {
		t.Fatalf("WaitSize = %d, want final delete intent", db.WaitSize())
	}
	state.DbProto.Value = "after-delete"
	if err := state.Save(ctx); err != nil {
		t.Fatalf("Save after delete error = %v", err)
	}
	if db.WaitSize() != 1 {
		t.Fatalf("WaitSize after deleted Save = %d, want final delete intent", db.WaitSize())
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("Flush error = %v", err)
	}

	loaded := &testSetState{}
	if err := loaded.Bind("player:delete", wrapperspb.String("default")); err != nil {
		t.Fatalf("loaded Bind error = %v", err)
	}
	ok, err := loaded.Load(ctx)
	if err != nil {
		t.Fatalf("Load after delete error = %v", err)
	}
	if ok {
		t.Fatal("Load after delete ok = true, want false")
	}
	if loaded.DbProto.GetValue() != "default" {
		t.Fatalf("missing Load changed default to %q", loaded.DbProto.GetValue())
	}
}

func TestHashSaverDifferentFieldsDoNotMerge(t *testing.T) {
	ctx := context.Background()
	db := newDelayedMemoryDB()
	installDefaultDB(t, db)
	first := &testHashState{}
	if err := first.Bind("rank", "uid:1", wrapperspb.String("one")); err != nil {
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
	if db.WaitSize() != 2 {
		t.Fatalf("WaitSize = %d, want 2 fields", db.WaitSize())
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("Flush error = %v", err)
	}

	loaded := &testHashState{}
	if err := loaded.Bind("rank", "uid:2", &wrapperspb.StringValue{}); err != nil {
		t.Fatalf("loaded Bind error = %v", err)
	}
	ok, err := loaded.Load(ctx)
	if err != nil || !ok {
		t.Fatalf("Load ok=%v err=%v, want true nil", ok, err)
	}
	if loaded.DbProto.GetValue() != "two" {
		t.Fatalf("loaded value = %q, want two", loaded.DbProto.GetValue())
	}
}

func TestHashSaverSameFieldMerges(t *testing.T) {
	ctx := context.Background()
	db := newDelayedMemoryDB()
	installDefaultDB(t, db)
	state := &testHashState{}
	if err := state.Bind("rank", "uid:1", wrapperspb.String("one")); err != nil {
		t.Fatalf("Bind error = %v", err)
	}
	if err := state.Save(ctx); err != nil {
		t.Fatalf("first Save error = %v", err)
	}
	state.DbProto.Value = "two"
	if err := state.Save(ctx); err != nil {
		t.Fatalf("second Save error = %v", err)
	}
	if db.WaitSize() != 1 {
		t.Fatalf("WaitSize = %d, want merged size 1", db.WaitSize())
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("Flush error = %v", err)
	}

	loaded := &testHashState{}
	if err := loaded.Bind("rank", "uid:1", &wrapperspb.StringValue{}); err != nil {
		t.Fatalf("loaded Bind error = %v", err)
	}
	ok, err := loaded.Load(ctx)
	if err != nil || !ok {
		t.Fatalf("Load ok=%v err=%v, want true nil", ok, err)
	}
	if loaded.DbProto.GetValue() != "two" {
		t.Fatalf("loaded value = %q, want two", loaded.DbProto.GetValue())
	}
}

func TestHashSaverTTLMergesExpireByHashKey(t *testing.T) {
	ctx := context.Background()
	db := newDelayedMemoryDB()
	installDefaultDB(t, db)
	first := &testHashState{}
	if err := first.Bind("rank:ttl-merge", "uid:1", wrapperspb.String("one"), WithTTL(time.Hour)); err != nil {
		t.Fatalf("Bind first error = %v", err)
	}
	if err := first.Save(ctx); err != nil {
		t.Fatalf("Save first error = %v", err)
	}
	second := &testHashState{}
	if err := second.Bind("rank:ttl-merge", "uid:2", wrapperspb.String("two"), WithTTL(time.Hour)); err != nil {
		t.Fatalf("Bind second error = %v", err)
	}
	if err := second.Save(ctx); err != nil {
		t.Fatalf("Save second error = %v", err)
	}
	if got, want := pendingIntentKeys(db.wait), []string{"hash:rank:ttl-merge:uid:1:save", "hash:rank:ttl-merge:uid:2:save"}; !equalStrings(got, want) {
		t.Fatalf("pending cache keys = %v, want %v", got, want)
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("Flush error = %v", err)
	}

	store := db.store.(*memoryStore)
	hash := store.hashes["rank:ttl-merge"]
	if hash.expiresAt.IsZero() {
		t.Fatal("hash expiresAt is zero after merged ttl save")
	}
	keys, err := store.HKeys(ctx, "rank:ttl-merge")
	if err != nil {
		t.Fatalf("HKeys error = %v", err)
	}
	if want := []string{"uid:1", "uid:2"}; !equalStrings(keys, want) {
		t.Fatalf("HKeys = %v, want %v", keys, want)
	}
}

func TestSaveUnsafeCanRunWhileLocked(t *testing.T) {
	ctx := context.Background()
	db := newDelayedMemoryDB()
	installDefaultDB(t, db)
	state := &testSetState{}
	if err := state.Bind("player:unsafe", wrapperspb.String("old")); err != nil {
		t.Fatalf("Bind error = %v", err)
	}
	state.Lock()
	state.DbProto.Value = "locked"
	if err := state.SaveUnsafe(ctx); err != nil {
		state.Unlock()
		t.Fatalf("SaveUnsafe error = %v", err)
	}
	state.Unlock()
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("Flush error = %v", err)
	}

	loaded := &testSetState{}
	if err := loaded.Bind("player:unsafe", &wrapperspb.StringValue{}); err != nil {
		t.Fatalf("loaded Bind error = %v", err)
	}
	ok, err := loaded.Load(ctx)
	if err != nil || !ok {
		t.Fatalf("Load ok=%v err=%v, want true nil", ok, err)
	}
	if loaded.DbProto.GetValue() != "locked" {
		t.Fatalf("loaded value = %q, want locked", loaded.DbProto.GetValue())
	}
}

func TestCloneDbProtoReturnsIndependentMessage(t *testing.T) {
	db := newDelayedMemoryDB()
	installDefaultDB(t, db)
	state := &testSetState{}
	if err := state.Bind("player:1", wrapperspb.String("original")); err != nil {
		t.Fatalf("Bind error = %v", err)
	}
	cloned := state.CloneDbProto()
	cloned.Value = "changed"
	if state.DbProto.GetValue() != "original" {
		t.Fatalf("DbProto value = %q, want original", state.DbProto.GetValue())
	}
}

func TestLoadUnmarshalErrorDoesNotReplaceDbProto(t *testing.T) {
	ctx := context.Background()
	db := newDelayedMemoryDB()
	installDefaultDB(t, db)
	store := db.store.(*memoryStore)
	store.kv["player:bad"] = record{data: []byte{0xff}}

	state := &testSetState{}
	if err := state.Bind("player:bad", wrapperspb.String("original")); err != nil {
		t.Fatalf("Bind error = %v", err)
	}
	ok, err := state.Load(ctx)
	if err == nil || ok {
		t.Fatalf("Load ok=%v err=%v, want false non-nil", ok, err)
	}
	if state.DbProto.GetValue() != "original" {
		t.Fatalf("DbProto value = %q, want original", state.DbProto.GetValue())
	}
}

func TestBindValidation(t *testing.T) {
	previous := DefaultDB()
	SetDefaultDB(nil)
	t.Cleanup(func() {
		SetDefaultDB(previous)
	})
	var nilMessage *wrapperspb.StringValue
	set := &testSetState{}
	if err := set.Bind("player:1", wrapperspb.String("x")); !errors.Is(err, ErrDBMissing) {
		t.Fatalf("Bind missing db error = %v, want %v", err, ErrDBMissing)
	}

	db := newDelayedMemoryDB()
	SetDefaultDB(db)
	t.Cleanup(func() {
		_ = db.Close(context.Background())
	})
	if err := set.Bind("", wrapperspb.String("x")); !errors.Is(err, ErrKeyMissing) {
		t.Fatalf("Bind empty key error = %v, want %v", err, ErrKeyMissing)
	}
	if err := set.Bind("player:1", nilMessage); !errors.Is(err, ErrMessageMissing) {
		t.Fatalf("Bind nil message error = %v, want %v", err, ErrMessageMissing)
	}
	if err := set.Bind("player:1", wrapperspb.String("x"), WithTTL(0)); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("Bind invalid ttl error = %v, want %v", err, ErrInvalidTTL)
	}

	hash := &testHashState{}
	if err := hash.Bind("rank", "", wrapperspb.String("x")); !errors.Is(err, ErrFieldMissing) {
		t.Fatalf("Bind empty field error = %v, want %v", err, ErrFieldMissing)
	}
}

func TestBindKeepsOriginalDefaultDB(t *testing.T) {
	ctx := context.Background()
	firstDB := newDelayedMemoryDB()
	secondDB := newDelayedMemoryDB()
	previous := DefaultDB()
	SetDefaultDB(firstDB)
	t.Cleanup(func() {
		_ = firstDB.Close(context.Background())
		_ = secondDB.Close(context.Background())
		SetDefaultDB(previous)
	})

	state := &testSetState{}
	if err := state.Bind("player:fixed-db", wrapperspb.String("initial")); err != nil {
		t.Fatalf("Bind error = %v", err)
	}

	SetDefaultDB(secondDB)
	state.DbProto.Value = "saved-to-first"
	if err := state.Save(ctx); err != nil {
		t.Fatalf("Save error = %v", err)
	}
	if err := firstDB.Flush(ctx); err != nil {
		t.Fatalf("first Flush error = %v", err)
	}
	if secondDB.WaitSize() != 0 {
		t.Fatalf("second DB WaitSize = %d, want 0", secondDB.WaitSize())
	}

	SetDefaultDB(firstDB)
	firstLoaded := &testSetState{}
	if err := firstLoaded.Bind("player:fixed-db", &wrapperspb.StringValue{}); err != nil {
		t.Fatalf("first loaded Bind error = %v", err)
	}
	ok, err := firstLoaded.Load(ctx)
	if err != nil || !ok {
		t.Fatalf("first Load ok=%v err=%v, want true nil", ok, err)
	}
	if firstLoaded.DbProto.GetValue() != "saved-to-first" {
		t.Fatalf("first loaded value = %q, want saved-to-first", firstLoaded.DbProto.GetValue())
	}

	SetDefaultDB(secondDB)
	secondLoaded := &testSetState{}
	if err := secondLoaded.Bind("player:fixed-db", wrapperspb.String("missing")); err != nil {
		t.Fatalf("second loaded Bind error = %v", err)
	}
	ok, err = secondLoaded.Load(ctx)
	if err != nil {
		t.Fatalf("second Load error = %v", err)
	}
	if ok {
		t.Fatal("second Load ok = true, want false")
	}
}

func TestCloseDefaultDBFlushesAndRejectsLaterSave(t *testing.T) {
	ctx := context.Background()
	db := newDelayedMemoryDB()
	SetDefaultDB(db)
	state := &testSetState{}
	if err := state.Bind("player:close", wrapperspb.String("saved")); err != nil {
		t.Fatalf("Bind error = %v", err)
	}
	if err := state.Save(ctx); err != nil {
		t.Fatalf("Save error = %v", err)
	}
	if err := CloseDefaultDB(ctx); err != nil {
		t.Fatalf("CloseDefaultDB error = %v", err)
	}
	if DefaultDB() != nil {
		t.Fatal("DefaultDB after CloseDefaultDB is not nil")
	}
	if err := state.Save(ctx); !errors.Is(err, ErrDBClosing) {
		t.Fatalf("Save after close error = %v, want %v", err, ErrDBClosing)
	}

	SetDefaultDB(db)
	loaded := &testSetState{}
	if err := loaded.Bind("player:close", &wrapperspb.StringValue{}); err != nil {
		t.Fatalf("loaded Bind error = %v", err)
	}
	ok, err := loaded.Load(ctx)
	SetDefaultDB(nil)
	if err != nil || !ok {
		t.Fatalf("Load after close ok=%v err=%v, want true nil", ok, err)
	}
	if loaded.DbProto.GetValue() != "saved" {
		t.Fatalf("loaded value = %q, want saved", loaded.DbProto.GetValue())
	}
}

func TestCloseIgnoresCanceledContextWhenFlushing(t *testing.T) {
	ctx := context.Background()
	db := newDelayedMemoryDB()
	installDefaultDB(t, db)
	state := &testSetState{}
	if err := state.Bind("player:close-canceled", wrapperspb.String("saved")); err != nil {
		t.Fatalf("Bind error = %v", err)
	}
	if err := state.Save(ctx); err != nil {
		t.Fatalf("Save error = %v", err)
	}
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := db.Close(canceledCtx); err != nil {
		t.Fatalf("Close canceled context error = %v, want nil", err)
	}
	if db.WaitSize() != 0 {
		t.Fatalf("WaitSize after Close = %d, want 0", db.WaitSize())
	}

	loaded := &testSetState{}
	if err := loaded.Bind("player:close-canceled", &wrapperspb.StringValue{}); err != nil {
		t.Fatalf("loaded Bind error = %v", err)
	}
	ok, err := loaded.Load(ctx)
	if err != nil || !ok {
		t.Fatalf("Load after canceled Close ok=%v err=%v, want true nil", ok, err)
	}
	if loaded.DbProto.GetValue() != "saved" {
		t.Fatalf("loaded value = %q, want saved", loaded.DbProto.GetValue())
	}
	if err := state.Save(ctx); !errors.Is(err, ErrDBClosing) {
		t.Fatalf("Save after Close error = %v, want %v", err, ErrDBClosing)
	}
}

func TestCloseReturnsErrorAndContinuesDrain(t *testing.T) {
	ctx := context.Background()
	store := &failFirstSetStore{values: make(map[string][]byte), failNextSet: true}
	db := newDB(store, options{minSaveDelay: time.Hour, maxSaveDelay: time.Hour})
	installDefaultDB(t, db)
	first := &testSetState{}
	if err := first.Bind("player:close-error:1", wrapperspb.String("first")); err != nil {
		t.Fatalf("Bind first error = %v", err)
	}
	if err := first.Save(ctx); err != nil {
		t.Fatalf("Save first error = %v", err)
	}
	second := &testSetState{}
	if err := second.Bind("player:close-error:2", wrapperspb.String("second")); err != nil {
		t.Fatalf("Bind second error = %v", err)
	}
	if err := second.Save(ctx); err != nil {
		t.Fatalf("Save second error = %v", err)
	}

	if err := db.Close(ctx); !errors.Is(err, errInjectedSetFailure) {
		t.Fatalf("Close error = %v, want %v", err, errInjectedSetFailure)
	}
	if db.WaitSize() != 0 {
		t.Fatalf("WaitSize after Close = %d, want 0", db.WaitSize())
	}
	if got, want := store.setKeys, []string{"player:close-error:1", "player:close-error:2"}; !equalStrings(got, want) {
		t.Fatalf("set attempts = %v, want %v", got, want)
	}
	if _, ok := store.values["player:close-error:2"]; !ok {
		t.Fatal("second key was not written after first write failed")
	}
	if err := first.Save(ctx); !errors.Is(err, ErrDBClosing) {
		t.Fatalf("Save after Close error = %v, want %v", err, ErrDBClosing)
	}
}

func TestWorkerDrainsAfterDelay(t *testing.T) {
	ctx := context.Background()
	db := MustNewMemoryDB(WithSaveDelay(time.Millisecond, time.Millisecond))
	installDefaultDB(t, db)
	state := &testSetState{}
	if err := state.Bind("player:delay", wrapperspb.String("saved")); err != nil {
		t.Fatalf("Bind error = %v", err)
	}
	if err := state.Save(ctx); err != nil {
		t.Fatalf("Save error = %v", err)
	}
	loaded := &testSetState{}
	if err := loaded.Bind("player:delay", &wrapperspb.StringValue{}); err != nil {
		t.Fatalf("loaded Bind error = %v", err)
	}
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		ok, err := loaded.Load(ctx)
		if err != nil {
			t.Fatalf("Load error = %v", err)
		}
		if ok && loaded.DbProto.GetValue() == "saved" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("worker did not drain queued save before deadline")
}
