package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-log"
	"github.com/li9chuan/aifirstdev/aifd-observe/contracts"
)

func TestTailReadsRealTextLogsWithEscapesAndFilters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	logger, closeLog, err := logging.NewLocalFirstLogger(logging.Config{LocalPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer closeLog()
	logger.Module("jobs").Info(context.Background(), "not selected")
	message := "import a=b \"quoted\"\n你好"
	logger.Module("jobs").Warn(context.Background(), message, logging.String("note", "spaces = quotes"))
	logger.Module("other").Warn(context.Background(), "not selected")
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}
	a, err := New(Config{Files: map[string]File{"log": {Path: path, Kind: contracts.KindLog}}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	result, err := a.Tail(context.Background(), TailQuery{Level: contracts.LevelWarn, Name: "jobs", Since: time.Now().Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 || result.Entries[0].Message != message || result.Entries[0].Time.IsZero() {
		t.Fatalf("entries = %+v", result.Entries)
	}
	result, err = a.Tail(context.Background(), TailQuery{Since: time.Now().Add(time.Minute)})
	if err != nil || len(result.Entries) != 0 {
		t.Fatalf("future since: %+v, %v", result, err)
	}
}

func TestParseTextAndHistoricalJSONAndRawCSV(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	path := filepath.Join(t.TempDir(), "text.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	handler := slog.NewTextHandler(f, nil)
	record := slog.NewRecord(now, slog.LevelError, "x=1 with spaces", 0)
	record.AddAttrs(slog.String("module", "jobs"), slog.String("a key", "ignored"))
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entry := parseEntry("log", File{Kind: contracts.KindLog}, string(raw))
	if !entry.Time.Equal(now) || entry.Level != contracts.LevelError || entry.Name != "jobs" || entry.Message != "x=1 with spaces" {
		t.Fatalf("text=%+v", entry)
	}
	entry = parseEntry("log", File{Kind: contracts.KindLog}, `{"time":"2026-01-01T00:00:00Z","level":"WARN","module":"jobs","msg":"old JSON"}`)
	if entry.Level != contracts.LevelWarn || entry.Name != "jobs" || entry.Message != "old JSON" || entry.Time.IsZero() {
		t.Fatalf("json=%+v", entry)
	}
	for _, text := range []string{`1,job_finished,1,1001,success`, `time="CSV header",value`, `time=broken level=INFO msg="unterminated`} {
		entry = parseEntry("stat", File{Kind: contracts.KindStat}, text)
		if entry.Text != text || !entry.Time.IsZero() || entry.Message != "" {
			t.Fatalf("raw=%+v", entry)
		}
	}
	if got := parseTextFields(`time=broken level=INFO msg="unterminated`); got != nil {
		t.Fatalf("truncated=%v", got)
	}
}
