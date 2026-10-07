package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-observe/contracts"
)

func TestTailFiltersLocalLogFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.log")
	content := "" +
		`{"time":"2026-06-17T01:00:00Z","level":"INFO","msg":"service started","module":"runtime"}` + "\n" +
		`{"time":"2026-06-17T01:01:00Z","level":"WARN","msg":"retry save","module":"rdb","key":"player:1"}` + "\n" +
		`{"time":"2026-06-17T01:02:00Z","level":"ERROR","msg":"flush failed","module":"rdb"}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write log file error = %v", err)
	}
	local, err := New(Config{
		Files: map[string]File{"log": {Path: path, Kind: contracts.KindLog}},
	})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}

	result, err := local.Tail(context.Background(), TailQuery{
		File:     "log",
		Level:    contracts.LevelWarn,
		Contains: "retry",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("Tail error = %v", err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("entry count = %d, want 1", len(result.Entries))
	}
	entry := result.Entries[0]
	if entry.Message != "retry save" || entry.Name != "rdb" || entry.Kind != contracts.KindLog {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.Level != contracts.LevelWarn {
		t.Fatalf("level = %s, want warn", entry.Level)
	}
}

func TestTailUsesLimitAndSince(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.log")
	content := "" +
		`{"time":"2026-06-17T01:00:00Z","level":"INFO","msg":"one"}` + "\n" +
		`{"time":"2026-06-17T01:01:00Z","level":"INFO","msg":"two"}` + "\n" +
		`{"time":"2026-06-17T01:02:00Z","level":"INFO","msg":"three"}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write log file error = %v", err)
	}
	local, err := New(Config{
		Files:    map[string]File{"log": {Path: path, Kind: contracts.KindLog}},
		MaxLines: 2,
	})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	since, _ := time.Parse(time.RFC3339, "2026-06-17T01:00:30Z")
	result, err := local.Tail(context.Background(), TailQuery{Since: since, Limit: 10})
	if err != nil {
		t.Fatalf("Tail error = %v", err)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("entry count = %d, want 2", len(result.Entries))
	}
	if result.Entries[0].Message != "two" || result.Entries[1].Message != "three" {
		t.Fatalf("entries = %+v", result.Entries)
	}
}

func TestTailRejectsUnregisteredFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.log")
	if err := os.WriteFile(path, []byte("line\n"), 0644); err != nil {
		t.Fatalf("write log error = %v", err)
	}
	local, err := New(Config{
		Files: map[string]File{"log": {Path: path, Kind: contracts.KindLog}},
	})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	if _, err := local.Tail(context.Background(), TailQuery{File: "missing"}); err == nil {
		t.Fatalf("Tail error = nil, want unregistered file error")
	}
}

func TestNewDerivesFileDirsFromFiles(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	statDir := filepath.Join(dir, "stats")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("mkdir log dir error = %v", err)
	}
	if err := os.MkdirAll(statDir, 0755); err != nil {
		t.Fatalf("mkdir stat dir error = %v", err)
	}
	logPath := filepath.Join(logDir, "service.log")
	statPath := filepath.Join(statDir, "service.stat")
	local, err := New(Config{
		Files: map[string]File{
			"log":  {Path: logPath, Kind: contracts.KindLog},
			"stat": {Path: statPath, Kind: contracts.KindStat},
		},
	})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	if len(local.fileDirs) != 2 {
		t.Fatalf("file dirs = %+v, want 2", local.fileDirs)
	}
	want := map[string]bool{
		filepath.Clean(logDir):  true,
		filepath.Clean(statDir): true,
	}
	for _, dir := range local.fileDirs {
		if !want[dir] {
			t.Fatalf("unexpected file dir %s in %+v", dir, local.fileDirs)
		}
	}
}

func TestStatsAndHealth(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.log")
	if err := os.WriteFile(path, []byte("line\n"), 0644); err != nil {
		t.Fatalf("write log file error = %v", err)
	}
	now := time.Date(2026, 6, 17, 1, 0, 0, 0, time.UTC)
	local, err := New(Config{
		Service:        "game",
		Instance:       "inst-1",
		Files:          map[string]File{"log": {Path: path, Kind: contracts.KindLog}},
		CollectRuntime: true,
		Now: func() time.Time {
			return now.Add(time.Second)
		},
	})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	if local.maxConcurrency != 1 || cap(local.queryTokens) != 1 {
		t.Fatalf("default max concurrency = %d cap=%d, want 1", local.maxConcurrency, cap(local.queryTokens))
	}
	if err := local.Write(context.Background(), []contracts.Event{
		{Kind: contracts.KindLog},
		{Kind: contracts.KindStat},
	}); err != nil {
		t.Fatalf("Write error = %v", err)
	}
	stats := local.Stats()
	if stats.Service != "game" || stats.Instance != "inst-1" || stats.Received != 2 {
		t.Fatalf("stats = %+v", stats)
	}
	if stats.ByKind[contracts.KindLog] != 1 || stats.ByKind[contracts.KindStat] != 1 {
		t.Fatalf("by kind = %+v", stats.ByKind)
	}
	if stats.Runtime.Goroutines == 0 {
		t.Fatalf("runtime stats missing goroutines: %+v", stats.Runtime)
	}
	health := local.Health()
	if !health.OK || len(health.Files) != 1 || !health.Files[0].Readable {
		t.Fatalf("health = %+v", health)
	}
}

func TestTailRejectsWhenConcurrencyLimitReached(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.log")
	if err := os.WriteFile(path, []byte(`{"time":"2026-06-17T01:00:00Z","level":"INFO","msg":"one"}`+"\n"), 0644); err != nil {
		t.Fatalf("write log file error = %v", err)
	}
	local, err := New(Config{
		Files:          map[string]File{"log": {Path: path, Kind: contracts.KindLog}},
		MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	if err := local.beginQuery(); err != nil {
		t.Fatalf("beginQuery error = %v", err)
	}
	stats := local.Stats()
	if stats.ActiveQueries != 1 || stats.RejectedQueries != 0 {
		t.Fatalf("stats before reject = %+v", stats)
	}

	_, err = local.Tail(context.Background(), TailQuery{File: "log"})
	if !errors.Is(err, ErrQueryConcurrencyLimit) {
		t.Fatalf("Tail error = %v, want ErrQueryConcurrencyLimit", err)
	}
	stats = local.Stats()
	if stats.ActiveQueries != 1 || stats.RejectedQueries != 1 {
		t.Fatalf("stats after reject = %+v", stats)
	}
	if stats.LastError != ErrQueryConcurrencyLimit.Error() {
		t.Fatalf("last error = %q", stats.LastError)
	}

	local.finishQuery(nil)
	result, err := local.Tail(context.Background(), TailQuery{File: "log"})
	if err != nil {
		t.Fatalf("Tail after release error = %v", err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("entry count = %d, want 1", len(result.Entries))
	}
	stats = local.Stats()
	if stats.ActiveQueries != 0 || stats.RejectedQueries != 1 {
		t.Fatalf("stats after release = %+v", stats)
	}
}
