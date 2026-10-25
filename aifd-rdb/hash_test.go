package rdb

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestLoadHashPersistAndIndependentPayloads(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			var db *DB
			if backend == "redis" {
				var cleanup func()
				db, _, cleanup = newRedisTestDB(t)
				defer cleanup()
			} else {
				db = MustNewMemoryDB(WithSaveDelay(time.Hour, time.Hour))
				defer db.Close(ctx)
			}
			for _, field := range []string{"a", "b"} {
				s := &HashSaver[*wrapperspb.StringValue]{}
				if err := s.BindDB(db, "hash", field, wrapperspb.String(field), WithTTL(time.Second)); err != nil {
					t.Fatal(err)
				}
				if err := s.Save(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			if err := db.Persist(ctx, "hash"); err != nil {
				t.Fatal(err)
			}
			switch store := db.store.(type) {
			case *memoryStore:
				store.mu.Lock()
				persistent := store.hashes["hash"].expiresAt.IsZero()
				store.mu.Unlock()
				if !persistent {
					t.Fatal("memory hash TTL was retained")
				}
			case *redisStore:
				if ttl := store.client.TTL(ctx, "hash").Val(); ttl != -1 {
					t.Fatalf("Redis TTL retained: %v", ttl)
				}
			}
			values, err := LoadHash(ctx, db, "hash", func() *wrapperspb.StringValue { return &wrapperspb.StringValue{} })
			if err != nil || len(values) != 2 {
				t.Fatal(values, err)
			}
			values["a"].Value = "mutated"
			again, err := LoadHash(ctx, db, "hash", func() *wrapperspb.StringValue { return &wrapperspb.StringValue{} })
			if err != nil || again["a"].GetValue() != "a" || values["b"].GetValue() != "b" {
				t.Fatal("decoded hash aliases state")
			}
			if err := db.store.HSet(ctx, "hash", "bad", []byte{0xff}); err != nil {
				t.Fatal(err)
			}
			if result, err := LoadHash(ctx, db, "hash", func() *wrapperspb.StringValue { return &wrapperspb.StringValue{} }); err == nil || result != nil {
				t.Fatal("partial corrupt hash returned")
			}
		})
	}
}

func TestHashLifecycleRejectsCanceledAndClosedWrites(t *testing.T) {
	db := MustNewMemoryDB()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := LoadHash(ctx, db, "hash", func() *wrapperspb.StringValue { return &wrapperspb.StringValue{} }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := db.Persist(ctx, "hash"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := db.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := db.Persist(context.Background(), "hash"); !errors.Is(err, ErrDBClosing) {
		t.Fatal(err)
	}
}
