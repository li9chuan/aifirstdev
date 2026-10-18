package rdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	observe "github.com/li9chuan/aifirstdev/aifd-observe/contracts"
	"github.com/li9chuan/aifirstdev/aifd-observe/logging"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestSaveLogUsesIntegerBytes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		hash  bool
		value string
		ttl   time.Duration
	}{
		{name: "set", value: strings.Repeat("x", 2048)},
		{name: "empty set"},
		{name: "hash", hash: true, value: strings.Repeat("x", 1536)},
		{name: "hash with ttl", hash: true, value: "value", ttl: time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := captureSaveLog(t)
			ctx := context.Background()
			db := MustNewMemoryDB(WithSaveDelay(time.Hour, time.Hour))
			t.Cleanup(func() { _ = db.Close(ctx) })
			pb := wrapperspb.String(tc.value)
			operation := "Set"
			if tc.hash {
				state := &testHashState{}
				var opts []Option
				if tc.ttl > 0 {
					opts = append(opts, WithTTL(tc.ttl))
				}
				if err := state.BindDB(db, "private-key", "private-field", pb, opts...); err != nil {
					t.Fatal(err)
				}
				if err := state.Save(ctx); err != nil {
					t.Fatal(err)
				}
				operation = "HSet"
			} else {
				state := &testSetState{}
				if err := state.BindDB(db, "private-key", pb); err != nil {
					t.Fatal(err)
				}
				if err := state.Save(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if output.Len() != 0 {
				t.Fatal("Save logged before the queued write was executed")
			}
			if err := db.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			var entry struct {
				Message string      `json:"msg"`
				Size    json.Number `json:"size"`
			}
			if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
				t.Fatalf("decode save log: %v; log=%s", err, output)
			}
			if entry.Message != "Rdb save "+operation || entry.Size.String() != strconv.Itoa(proto.Size(pb)) {
				t.Fatalf("save log = %s, want %s size %d bytes", output, operation, proto.Size(pb))
			}
			if strings.Contains(output.String(), "private-key") || strings.Contains(output.String(), "private-field") {
				t.Fatalf("save log exposes raw key or field: %s", output)
			}
		})
	}
}

func TestFailedWriteDoesNotLogSaveSuccess(t *testing.T) {
	output := captureSaveLog(t)
	ctx := context.Background()
	db := newDB(alwaysRetrySetStore{}, options{failureFileDir: t.TempDir()})
	t.Cleanup(func() { _ = db.Close(ctx) })
	state := &testSetState{}
	if err := state.BindDB(db, "failed-key", wrapperspb.String("value")); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Flush(ctx); err == nil {
		t.Fatal("Flush succeeded with a failing store")
	}
	if output.Len() != 0 {
		t.Fatalf("failed write logged save success: %s", output)
	}
}

func captureSaveLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	previous := logging.Default()
	t.Cleanup(func() { logging.SetDefault(previous) })
	output := &bytes.Buffer{}
	logging.SetDefault(logging.NewSlogLogger(slog.NewJSONHandler(output, nil)))
	return output
}

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
