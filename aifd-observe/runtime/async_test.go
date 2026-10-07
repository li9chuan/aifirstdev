package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-observe/adapters/memory"
	"github.com/li9chuan/aifirstdev/aifd-observe/contracts"
)

func TestAsyncSinkFlushesBatch(t *testing.T) {
	recorder := memory.NewRecorder()
	async := NewAsyncSink(recorder, AsyncOptions{QueueSize: 4, BatchSize: 2, FlushInterval: time.Hour})
	async.Start(context.Background())
	if err := async.Emit(contracts.Log(contracts.LevelInfo, "test.one", "one")); err != nil {
		t.Fatalf("Emit one error = %v", err)
	}
	if err := async.Emit(contracts.Log(contracts.LevelInfo, "test.two", "two")); err != nil {
		t.Fatalf("Emit two error = %v", err)
	}
	async.Close()

	events := recorder.Events()
	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2", len(events))
	}
}

func TestAsyncSinkDropsWhenFull(t *testing.T) {
	async := NewAsyncSink(nil, AsyncOptions{QueueSize: 1, BatchSize: 64, FlushInterval: time.Hour})
	if err := async.Emit(contracts.Log(contracts.LevelInfo, "test.one", "one")); err != nil {
		t.Fatalf("Emit one error = %v", err)
	}
	if err := async.Emit(contracts.Log(contracts.LevelInfo, "test.two", "two")); err != nil {
		t.Fatalf("Emit two error = %v", err)
	}
	if async.Dropped() != 1 {
		t.Fatalf("Dropped = %d, want 1", async.Dropped())
	}
	async.Close()
}
