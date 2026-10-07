package logging

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-observe/adapters/memory"
	"github.com/li9chuan/aifirstdev/aifd-observe/contracts"
)

func TestNewLocalFirstLoggerRejectsUnopenableLocalPath(t *testing.T) {
	dir := t.TempDir()
	logger, closeFn, err := NewLocalFirstLogger(Config{LocalPath: dir})
	if err == nil {
		if closeFn != nil {
			_ = closeFn()
		}
		t.Fatalf("NewLocalFirstLogger logger=%v err=nil, want error", logger)
	}
}

func TestInitDefaultWritesLocalJSONFile(t *testing.T) {
	previous := Default()
	previousSink := DefaultSink()
	t.Cleanup(func() {
		SetDefault(previous)
		SetDefaultSink(previousSink)
		setDefaultSource("", "")
	})
	path := filepath.Join(t.TempDir(), "service.log")
	closeFn, err := InitDefault(Config{
		LocalPath:      path,
		SourceService:  "minigame",
		SourceInstance: "inst-1",
	})
	if err != nil {
		t.Fatalf("InitDefault error = %v", err)
	}
	Info("service started", String("stage", "start"))
	if err := closeFn(); err != nil {
		t.Fatalf("close logger error = %v", err)
	}

	entry := readSingleLogEntry(t, path)
	if entry["msg"] != "service started" {
		t.Fatalf("msg = %v, want service started", entry["msg"])
	}
	if entry["source_service"] != "minigame" || entry["source_instance"] != "inst-1" {
		t.Fatalf("source fields = %v/%v, want minigame/inst-1", entry["source_service"], entry["source_instance"])
	}
	if entry["stage"] != "start" {
		t.Fatalf("stage = %v, want start", entry["stage"])
	}
}

func TestObserveFailureDoesNotBlockLocalWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	logger, closeFn, err := NewLocalFirstLogger(Config{
		LocalPath:      path,
		SourceService:  "svc",
		SourceInstance: "inst",
		Observe:        failingSink{},
	})
	if err != nil {
		t.Fatalf("NewLocalFirstLogger error = %v", err)
	}
	logger.Info(context.Background(), "local survives observe failure", String("stage", "observe"))
	if err := closeFn(); err != nil {
		t.Fatalf("close logger error = %v", err)
	}

	entry := readSingleLogEntry(t, path)
	if entry["msg"] != "local survives observe failure" {
		t.Fatalf("msg = %v, want local survives observe failure", entry["msg"])
	}
	if entry["stage"] != "observe" {
		t.Fatalf("stage = %v, want observe", entry["stage"])
	}
}

func TestPackageHelpersWriteWithoutContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	logger, closeFn, err := NewLocalFirstLogger(Config{LocalPath: path})
	if err != nil {
		t.Fatalf("NewLocalFirstLogger error = %v", err)
	}
	previous := Default()
	t.Cleanup(func() {
		SetDefault(previous)
	})
	SetDefault(logger)

	Info("service started", String("stage", "start"))
	Warnf("retry %s", "save")
	if err := closeFn(); err != nil {
		t.Fatalf("close logger error = %v", err)
	}

	entries := readLogEntries(t, path)
	if len(entries) != 2 {
		t.Fatalf("entry count = %d, want 2", len(entries))
	}
	if entries[0]["msg"] != "service started" || entries[0]["stage"] != "start" {
		t.Fatalf("first entry = %v", entries[0])
	}
	if entries[1]["msg"] != "retry save" || entries[1]["level"] != "WARN" {
		t.Fatalf("second entry = %v", entries[1])
	}
}

func TestStatWritesDefaultSink(t *testing.T) {
	recorder := memory.NewRecorder()
	previousSink := DefaultSink()
	t.Cleanup(func() {
		SetDefaultSink(previousSink)
		setDefaultSource("", "")
	})
	SetDefaultSink(recorder)
	setDefaultSource("game", "inst-1")

	Stat("friend.add", Int64("uid", 1001), String("trace_id", "trace-1"), String("shard_key", "uid:1001"))
	Statf("type=%s uid=%d", "legacy", 1001)

	events := recorder.Events()
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2", len(events))
	}
	if events[0].Kind != contracts.KindStat || events[0].Name != "friend.add" || events[0].Value != 1 {
		t.Fatalf("first event = %+v", events[0])
	}
	if events[0].SourceService != "game" || events[0].SourceInstance != "inst-1" {
		t.Fatalf("source = %s/%s, want game/inst-1", events[0].SourceService, events[0].SourceInstance)
	}
	if events[0].TraceID != "trace-1" || events[0].ShardKey != "uid:1001" {
		t.Fatalf("trace/shard = %s/%s", events[0].TraceID, events[0].ShardKey)
	}
	if events[0].Fields["uid"].(int64) != 1001 {
		t.Fatalf("uid field = %v, want 1001", events[0].Fields["uid"])
	}
	if events[1].Kind != contracts.KindStat || events[1].Name != "stat.raw" || events[1].Message != "type=legacy uid=1001" {
		t.Fatalf("second event = %+v", events[1])
	}
}

func TestStatNoopsWithoutDefaultSink(t *testing.T) {
	previousSink := DefaultSink()
	t.Cleanup(func() {
		SetDefaultSink(previousSink)
	})
	SetDefaultSink(nil)

	Stat("missing.sink", String("key", "value"))
	Statf("raw %s", "value")
}

func TestLoggerFieldsModuleAndLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	logger, closeFn, err := NewLocalFirstLogger(Config{
		LocalPath:      path,
		Level:          LevelDebug,
		SourceService:  "svc",
		SourceInstance: "inst",
	})
	if err != nil {
		t.Fatalf("NewLocalFirstLogger error = %v", err)
	}
	logger.Module("rdb").With(String("stage", "flush")).Error(
		context.Background(),
		"flush failed",
		Int64("uid", 1001),
		Bool("retry", true),
		Duration("elapsed", 3*time.Second),
		Err(errors.New("redis down")),
	)
	if err := closeFn(); err != nil {
		t.Fatalf("close logger error = %v", err)
	}

	entry := readSingleLogEntry(t, path)
	if entry["level"] != "ERROR" || entry["module"] != "rdb" || entry["stage"] != "flush" {
		t.Fatalf("entry = %v, want error rdb flush", entry)
	}
	if entry["err"] != "redis down" {
		t.Fatalf("err = %v, want redis down", entry["err"])
	}
	if entry["uid"].(float64) != 1001 {
		t.Fatalf("uid = %v, want 1001", entry["uid"])
	}
	if entry["retry"] != true {
		t.Fatalf("retry = %v, want true", entry["retry"])
	}
}

func TestSinkHandlerCreatesLogEvent(t *testing.T) {
	recorder := memory.NewRecorder()
	logger := NewSlogLogger(NewSinkHandler(recorder)).
		Module("runtime").
		With(String("source_service", "svc"), String("source_instance", "inst"))
	logger.Warn(context.Background(), "recover skipped", String("trace_id", "trace-1"), String("shard_key", "sid:1"))

	events := recorder.Events()
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	event := events[0]
	if event.Kind != contracts.KindLog || event.Level != contracts.LevelWarn || event.Name != "runtime" {
		t.Fatalf("event = %+v, want warn log runtime", event)
	}
	if event.Message != "recover skipped" || event.SourceService != "svc" || event.SourceInstance != "inst" {
		t.Fatalf("event message/source = %+v", event)
	}
	if event.TraceID != "trace-1" || event.ShardKey != "sid:1" {
		t.Fatalf("trace/shard = %q/%q, want trace-1/sid:1", event.TraceID, event.ShardKey)
	}
	if event.Fields["module"] != "runtime" {
		t.Fatalf("event module field = %v, want runtime", event.Fields["module"])
	}
}

func TestNewTeeLoggerRejectsMissingLocalHandler(t *testing.T) {
	logger, err := NewTeeLogger(nil, slog.NewJSONHandler(os.Stdout, nil))
	if !errors.Is(err, errLocalHandlerMissing) {
		t.Fatalf("NewTeeLogger logger=%v err=%v, want %v", logger, err, errLocalHandlerMissing)
	}
}

type failingSink struct{}

func (failingSink) Write(context.Context, []contracts.Event) error {
	return errors.New("observe failed")
}

func readSingleLogEntry(t *testing.T, path string) map[string]any {
	t.Helper()
	entries := readLogEntries(t, path)
	if len(entries) != 1 {
		t.Fatalf("entry count = %d, want 1", len(entries))
	}
	return entries[0]
}

func readLogEntries(t *testing.T, path string) []map[string]any {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open log file error = %v", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	var entries []map[string]any
	for scanner.Scan() {
		entry := make(map[string]any)
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("unmarshal log line error = %v line=%s", err, scanner.Text())
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan log file error = %v", err)
	}
	return entries
}
