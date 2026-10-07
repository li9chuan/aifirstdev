package rdb

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	observe "github.com/li9chuan/aifirstdev/aifd-observe/contracts"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type observeRecorder struct {
	mu     sync.Mutex
	events []observe.Event
}

func (r *observeRecorder) Write(_ context.Context, events []observe.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, event := range events {
		r.events = append(r.events, event.Clone())
	}
	return nil
}

func (r *observeRecorder) has(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, event := range r.events {
		if event.Name == name {
			return true
		}
	}
	return false
}

func (r *observeRecorder) event(name string) (observe.Event, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, event := range r.events {
		if event.Name == name {
			return event.Clone(), true
		}
	}
	return observe.Event{}, false
}

func TestObserveReportsQueueDurationAndLargePayload(t *testing.T) {
	ctx := context.Background()
	recorder := &observeRecorder{}
	db := MustNewMemoryDB(
		WithSaveDelay(time.Hour, time.Hour),
		WithObserveSink(recorder),
		WithLargePayloadThresholds(1, 1),
	)
	defer db.Close(ctx)

	state := &testSetState{}
	if err := state.BindDB(db, "player:observe", wrapperspb.String("large-value")); err != nil {
		t.Fatalf("BindDB error = %v", err)
	}
	if err := state.Save(ctx); err != nil {
		t.Fatalf("Save error = %v", err)
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("Flush error = %v", err)
	}

	for _, name := range []string{metricQueueWait, metricSaveDuration, metricPayloadLarge} {
		if !recorder.has(name) {
			t.Fatalf("observe event %s missing; events=%v", name, recorder.events)
		}
	}
	event, ok := recorder.event(metricPayloadLarge)
	if !ok {
		t.Fatal("large payload event missing")
	}
	if _, ok := event.Fields["key"]; ok {
		t.Fatalf("large payload fields expose raw key: %v", event.Fields)
	}
	if _, ok := event.Fields["key_hash"]; !ok {
		t.Fatalf("large payload fields missing key_hash: %v", event.Fields)
	}
	if _, ok := event.Fields["payload_bytes"]; !ok {
		t.Fatalf("large payload fields missing payload_bytes: %v", event.Fields)
	}
}

func TestObserveRawKeysIsExplicitOptIn(t *testing.T) {
	ctx := context.Background()
	recorder := &observeRecorder{}
	db := MustNewMemoryDB(
		WithSaveDelay(time.Hour, time.Hour),
		WithObserveSink(recorder),
		WithObserveRawKeys(),
		WithLargePayloadThresholds(1, 1),
	)
	defer db.Close(ctx)

	state := &testSetState{}
	if err := state.BindDB(db, "player:raw-observe", wrapperspb.String("large-value")); err != nil {
		t.Fatalf("BindDB error = %v", err)
	}
	if err := state.Save(ctx); err != nil {
		t.Fatalf("Save error = %v", err)
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatalf("Flush error = %v", err)
	}

	event, ok := recorder.event(metricPayloadLarge)
	if !ok {
		t.Fatal("large payload event missing")
	}
	if got := event.Fields["key"]; got != "player:raw-observe" {
		t.Fatalf("raw key field = %v, want player:raw-observe", got)
	}
	if _, ok := event.Fields["key_hash"]; ok {
		t.Fatalf("raw key event also includes key_hash: %v", event.Fields)
	}
}

func TestReportErrorIgnoresNilError(t *testing.T) {
	recorder := &observeRecorder{}
	db := MustNewMemoryDB(WithObserveSink(recorder))
	defer db.Close(context.Background())

	db.reportError(context.Background(), "worker", setDeleteIntent("player:nil-error"), 0, false, false, nil)

	if recorder.has(metricSaveError) {
		t.Fatalf("nil reportError emitted save error: %v", recorder.events)
	}
}

type alwaysRetrySetStore struct{}

func (s alwaysRetrySetStore) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, nil
}

func (s alwaysRetrySetStore) Set(context.Context, string, []byte, time.Duration) error {
	return errors.New("connection refused")
}

func (s alwaysRetrySetStore) Delete(context.Context, string) error {
	return nil
}

func (s alwaysRetrySetStore) Expire(context.Context, string, time.Duration) error {
	return nil
}

func (s alwaysRetrySetStore) HGet(context.Context, string, string) ([]byte, bool, error) {
	return nil, false, nil
}

func (s alwaysRetrySetStore) HSet(context.Context, string, string, []byte) error {
	return nil
}

func (s alwaysRetrySetStore) HDelete(context.Context, string, string) error {
	return nil
}

func (s alwaysRetrySetStore) HKeys(context.Context, string) ([]string, error) {
	return nil, nil
}

func TestObserveReportsRetryAndCloseJournal(t *testing.T) {
	ctx := context.Background()
	recorder := &observeRecorder{}
	db := newDB(alwaysRetrySetStore{}, options{
		minSaveDelay:          time.Hour,
		maxSaveDelay:          time.Hour,
		minRetryDelay:         time.Millisecond,
		maxRetryDelay:         time.Millisecond,
		failureFileDir:        t.TempDir(),
		observeSink:           recorder,
		largeSetPayloadBytes:  defaultLargeSetPayloadBytes,
		largeHashPayloadBytes: defaultLargeHashPayloadBytes,
	})

	state := &testSetState{}
	if err := state.BindDB(db, "player:retry-observe", wrapperspb.String("value")); err != nil {
		t.Fatalf("BindDB error = %v", err)
	}
	if err := state.Save(ctx); err != nil {
		t.Fatalf("Save error = %v", err)
	}
	if err := db.Close(ctx); err == nil {
		t.Fatal("Close error = nil, want retryable store error")
	}

	for _, name := range []string{metricSaveError, metricSaveRetry, metricCloseJournal} {
		if !recorder.has(name) {
			t.Fatalf("observe event %s missing; events=%v", name, recorder.events)
		}
	}
}
