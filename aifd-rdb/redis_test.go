package rdb

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	redisgo "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func newRedisTestDB(t *testing.T) (*DB, *miniredis.Miniredis, func()) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redisgo.NewClient(&redisgo.Options{Addr: server.Addr()})
	db, err := NewRedisDB(client, WithSaveDelay(time.Hour, time.Hour))
	if err != nil {
		t.Fatalf("NewRedisDB error = %v", err)
	}
	cleanup := func() {
		_ = db.Close(context.Background())
		_ = client.Close()
		server.Close()
	}
	return db, server, cleanup
}

func TestNewRedisDBRejectsNilClient(t *testing.T) {
	db, err := NewRedisDB(nil)
	if !errors.Is(err, ErrRedisClientMissing) {
		t.Fatalf("NewRedisDB error = %v, want %v", err, ErrRedisClientMissing)
	}
	if db != nil {
		t.Fatalf("NewRedisDB db = %#v, want nil", db)
	}
}

func TestNewRedisDBStartupReplay(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	raw, err := proto.Marshal(wrapperspb.String("replayed"))
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}
	if err := writeJournalFile(dir, journalSourceClose, []journalRecord{{
		Source:      journalSourceClose,
		Kind:        intentKindSet,
		Intent:      intentSave,
		Key:         "player:startup-replay",
		ValueBase64: base64.StdEncoding.EncodeToString(raw),
		Ts:          time.Now().UnixMilli(),
	}}); err != nil {
		t.Fatalf("writeJournalFile error = %v", err)
	}

	server := miniredis.RunT(t)
	client := redisgo.NewClient(&redisgo.Options{Addr: server.Addr()})
	db, err := NewRedisDB(client, WithFailureFileDir(dir), WithStartupReplay(), WithSaveDelay(time.Hour, time.Hour))
	if err != nil {
		t.Fatalf("NewRedisDB error = %v", err)
	}
	defer func() {
		_ = db.Close(context.Background())
		_ = client.Close()
		server.Close()
	}()
	installDefaultDB(t, db)
	loaded := &testSetState{}
	if err := loaded.Bind("player:startup-replay", &wrapperspb.StringValue{}); err != nil {
		t.Fatalf("Bind error = %v", err)
	}
	ok, err := loaded.Load(ctx)
	if err != nil || !ok {
		t.Fatalf("Load ok=%v err=%v, want true nil", ok, err)
	}
	if loaded.DbProto.GetValue() != "replayed" {
		t.Fatalf("loaded value = %q, want replayed", loaded.DbProto.GetValue())
	}
	if len(findJournalFiles(t, dir, ".done")) != 1 {
		t.Fatalf("done files = %v, want one", findJournalFiles(t, dir, ".done"))
	}
}

func TestRedisSetDelayedSaveLoadDelete(t *testing.T) {
	ctx := context.Background()
	db, _, cleanup := newRedisTestDB(t)
	defer cleanup()
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
	if !loaded.Delete(ctx) {
		t.Fatal("Delete = false, want true")
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("Flush delete error = %v", err)
	}
	ok, err = loaded.Load(ctx)
	if err != nil {
		t.Fatalf("Load after delete error = %v", err)
	}
	if ok {
		t.Fatal("Load after delete ok = true, want false")
	}
}

func TestRedisSetKeepTTL(t *testing.T) {
	ctx := context.Background()
	db, server, cleanup := newRedisTestDB(t)
	defer cleanup()
	installDefaultDB(t, db)
	state := &testSetState{}
	if err := state.Bind("player:ttl", wrapperspb.String("old"), WithTTL(10*time.Second)); err != nil {
		t.Fatalf("Bind initial error = %v", err)
	}
	if err := state.Save(ctx); err != nil {
		t.Fatalf("Save initial error = %v", err)
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("Flush initial error = %v", err)
	}
	next := &testSetState{}
	if err := next.Bind("player:ttl", wrapperspb.String("new"), KeepTTL()); err != nil {
		t.Fatalf("Bind keep ttl error = %v", err)
	}
	if err := next.Save(ctx); err != nil {
		t.Fatalf("Save keep ttl error = %v", err)
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("Flush keep ttl error = %v", err)
	}
	server.FastForward(11 * time.Second)

	loaded := &testSetState{}
	if err := loaded.Bind("player:ttl", wrapperspb.String("default")); err != nil {
		t.Fatalf("loaded Bind error = %v", err)
	}
	ok, err := loaded.Load(ctx)
	if err != nil {
		t.Fatalf("Load after ttl error = %v", err)
	}
	if ok {
		t.Fatal("Load after preserved ttl ok = true, want false")
	}
}

func TestRedisHashSaveLoadDeleteKeys(t *testing.T) {
	ctx := context.Background()
	db, _, cleanup := newRedisTestDB(t)
	defer cleanup()
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
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	keys, err := db.store.HKeys(ctx, "rank")
	if err != nil {
		t.Fatalf("HKeys error = %v", err)
	}
	if want := []string{"uid:1", "uid:2"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("HKeys = %v, want %v", keys, want)
	}

	loaded := &testHashState{}
	if err := loaded.Bind("rank", "uid:1", &wrapperspb.StringValue{}); err != nil {
		t.Fatalf("loaded Bind error = %v", err)
	}
	ok, err := loaded.Load(ctx)
	if err != nil || !ok {
		t.Fatalf("Load ok=%v err=%v, want true nil", ok, err)
	}
	if loaded.DbProto.GetValue() != "one" {
		t.Fatalf("loaded value = %q, want one", loaded.DbProto.GetValue())
	}
	if !loaded.Delete(ctx) {
		t.Fatal("Delete = false, want true")
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("Flush delete error = %v", err)
	}
	ok, err = loaded.Load(ctx)
	if err != nil {
		t.Fatalf("Load after delete error = %v", err)
	}
	if ok {
		t.Fatal("Load after delete ok = true, want false")
	}
}

func TestRedisHashTTLAppliesToHashKey(t *testing.T) {
	ctx := context.Background()
	db, server, cleanup := newRedisTestDB(t)
	defer cleanup()
	installDefaultDB(t, db)
	first := &testHashState{}
	if err := first.Bind("rank:ttl", "uid:1", wrapperspb.String("one"), WithTTL(10*time.Second)); err != nil {
		t.Fatalf("Bind first error = %v", err)
	}
	if err := first.Save(ctx); err != nil {
		t.Fatalf("Save first error = %v", err)
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("Flush first error = %v", err)
	}
	second := &testHashState{}
	if err := second.Bind("rank:ttl", "uid:2", wrapperspb.String("two")); err != nil {
		t.Fatalf("Bind second error = %v", err)
	}
	if err := second.Save(ctx); err != nil {
		t.Fatalf("Save second error = %v", err)
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("Flush second error = %v", err)
	}
	server.FastForward(11 * time.Second)
	keys, err := db.store.HKeys(ctx, "rank:ttl")
	if err != nil {
		t.Fatalf("HKeys after ttl error = %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("HKeys after hash ttl = %v, want empty", keys)
	}
}

func TestRedisHashTTLMergesExpireByHashKey(t *testing.T) {
	ctx := context.Background()
	db, server, cleanup := newRedisTestDB(t)
	defer cleanup()
	installDefaultDB(t, db)
	first := &testHashState{}
	if err := first.Bind("rank:ttl-merge", "uid:1", wrapperspb.String("one"), WithTTL(10*time.Second)); err != nil {
		t.Fatalf("Bind first error = %v", err)
	}
	if err := first.Save(ctx); err != nil {
		t.Fatalf("Save first error = %v", err)
	}
	second := &testHashState{}
	if err := second.Bind("rank:ttl-merge", "uid:2", wrapperspb.String("two"), WithTTL(10*time.Second)); err != nil {
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
	if ttl := server.TTL("rank:ttl-merge"); ttl <= 0 {
		t.Fatalf("TTL = %v, want positive", ttl)
	}
	keys, err := db.store.HKeys(ctx, "rank:ttl-merge")
	if err != nil {
		t.Fatalf("HKeys error = %v", err)
	}
	if want := []string{"uid:1", "uid:2"}; !equalStrings(keys, want) {
		t.Fatalf("HKeys = %v, want %v", keys, want)
	}
}

func TestRedisCloseFlushesQueuedSave(t *testing.T) {
	ctx := context.Background()
	db, _, cleanup := newRedisTestDB(t)
	defer cleanup()
	installDefaultDB(t, db)
	state := &testSetState{}
	if err := state.Bind("player:close", wrapperspb.String("saved")); err != nil {
		t.Fatalf("Bind error = %v", err)
	}
	if err := state.Save(ctx); err != nil {
		t.Fatalf("Save error = %v", err)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	loaded := &testSetState{}
	if err := loaded.Bind("player:close", &wrapperspb.StringValue{}); err != nil {
		t.Fatalf("loaded Bind error = %v", err)
	}
	ok, err := loaded.Load(ctx)
	if err != nil || !ok {
		t.Fatalf("Load after Close ok=%v err=%v, want true nil", ok, err)
	}
	if loaded.DbProto.GetValue() != "saved" {
		t.Fatalf("loaded value = %q, want saved", loaded.DbProto.GetValue())
	}
}
