package loggingsink

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/li9chuan/aifirstdev/aifd-log"
	"github.com/li9chuan/aifirstdev/aifd-observe/adapters/memory"
	"github.com/li9chuan/aifirstdev/aifd-observe/contracts"
)

func TestHandlerCreatesObserveEventsWithSourceAndGroups(t *testing.T) {
	recorder := memory.NewRecorder()
	logger := logging.NewSlogLogger(NewHandler(recorder)).Module("runtime").With(
		logging.String("source_service", "svc"), logging.String("source_instance", "inst"),
	)
	logger.Warn(context.Background(), "recover skipped", logging.String("trace_id", "trace-1"), logging.String("shard_key", "sid:1"))
	events := recorder.Events()
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	event := events[0]
	if event.Kind != contracts.KindLog || event.Level != contracts.LevelWarn || event.Name != "runtime" || event.Message != "recover skipped" || event.SourceService != "svc" || event.SourceInstance != "inst" || event.TraceID != "trace-1" || event.ShardKey != "sid:1" {
		t.Fatalf("event = %+v", event)
	}
	slog.New(NewHandler(recorder)).With("outer", "kept").WithGroup("request").With("uid", 1001).Info("grouped", "ready", true)
	event = recorder.Events()[1]
	if event.Fields["outer"] != "kept" || event.Fields["request.uid"] != int64(1001) || event.Fields["request.ready"] != true {
		t.Fatalf("group fields = %+v", event.Fields)
	}
}

func TestOptionalAdapterFailureKeepsLocalText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	logger, closeLog, err := logging.NewLocalFirstLogger(logging.Config{
		LocalPath: path, SecondaryHandler: NewHandler(failingSink{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeLog() })
	logger.Info(context.Background(), "saved locally")
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), `msg="saved locally"`) {
		t.Fatalf("local = %q, %v", raw, err)
	}
}

type failingSink struct{}

func (failingSink) Write(context.Context, []contracts.Event) error {
	return errors.New("receiver unavailable")
}
