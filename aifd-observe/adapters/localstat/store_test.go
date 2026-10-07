package localstat

import (
	"context"
	"testing"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-observe/contracts"
)

func TestStoreDeduplicatesAndAggregatesStats(t *testing.T) {
	store := NewStore(time.Minute)
	now := time.Unix(100, 0).UTC()
	event := contracts.Stat("event-1", "battle_win", "uid:1", 1, map[string]string{"sid": "1"})
	event.Time = now

	if err := store.Write(context.Background(), []contracts.Event{event, event}); err != nil {
		t.Fatalf("Write error = %v", err)
	}
	if raw := store.RawEvents(); len(raw) != 1 {
		t.Fatalf("raw len = %d, want 1", len(raw))
	}
	aggs := store.Aggregates()
	if len(aggs) != 1 {
		t.Fatalf("aggs len = %d, want 1", len(aggs))
	}
	for _, value := range aggs {
		if value != 1 {
			t.Fatalf("aggregate value = %v, want 1", value)
		}
	}
}
