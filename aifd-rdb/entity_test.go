package rdb

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestHashSavesKeepDistinctKeyFieldBoundaries(t *testing.T) {
	for _, stage := range []string{"queued", "retrying", "in-flight"} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			store := newScriptedStore()
			var first, second HashSaver[*wrapperspb.StringValue]
			var handler ErrorHandler
			if stage == "in-flight" {
				handler = func(context.Context, ErrorEvent) {
					if err := second.Save(ctx); err != nil {
						t.Fatalf("Save during failed write = %v", err)
					}
				}
			}
			db := newJournalTestDB(store, t.TempDir(), handler)
			defer db.Close(ctx)
			if err := first.BindDB(db, "a:b", "c", wrapperspb.String("first")); err != nil {
				t.Fatal(err)
			}
			if err := second.BindDB(db, "a", "b:c", wrapperspb.String("second")); err != nil {
				t.Fatal(err)
			}
			if err := first.Save(ctx); err != nil {
				t.Fatal(err)
			}
			if stage != "queued" {
				store.failHSet = 1
				if err := db.Flush(ctx); !errors.Is(err, errRetryableWriteFailure) {
					t.Fatalf("first Flush = %v, want retryable failure", err)
				}
			}
			if stage != "in-flight" {
				if err := second.Save(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			for _, record := range []struct {
				key, field, value string
			}{
				{"a:b", "c", "first"},
				{"a", "b:c", "second"},
			} {
				var loaded HashSaver[*wrapperspb.StringValue]
				if err := loaded.BindDB(db, record.key, record.field, wrapperspb.String("")); err != nil {
					t.Fatal(err)
				}
				if ok, err := loaded.Load(ctx); err != nil || !ok {
					t.Fatalf("Load(%q, %q) = %v, %v; independent record lost", record.key, record.field, ok, err)
				}
				if got := loaded.DbProto.GetValue(); got != record.value {
					t.Fatalf("Load(%q, %q) value = %q, want %q", record.key, record.field, got, record.value)
				}
			}
		})
	}
}
