package logging

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"testing"
	"time"
)

func TestLocalLogPathsStayAnchoredAfterChdir(t *testing.T) {
	original, changed := t.TempDir(), t.TempDir()
	t.Chdir(original)
	_, closeLog := initStatTest(t, StatConfig{LocalPath: filepath.Join("logs", "stat.log")})
	Info("before chdir")
	if err := RecordStat(JobFinished{"before", 1}, statTestBuild); err != nil {
		t.Fatal(err)
	}
	t.Chdir(changed)
	Info("after chdir")
	if err := RecordStat(JobFinished{"after", 2}, statTestBuild); err != nil {
		t.Fatal(err)
	}
	if err := closeLog(); err != nil {
		t.Fatalf("close after chdir: %v", err)
	}
	text := readLogText(t, filepath.Join(original, "logs", "run.log"))
	if !strings.Contains(text, "before chdir") || !strings.Contains(text, "after chdir") {
		t.Fatalf("ordinary records = %s", text)
	}
	rows := readStatRows(t, filepath.Join(original, "logs", "stat.log"))
	if len(rows) != 2 || rows[0][5] != "before" || rows[1][5] != "after" {
		t.Fatalf("Stat records = %#v", rows)
	}
	if _, err := os.Stat(filepath.Join(changed, "logs")); !os.IsNotExist(err) {
		t.Fatalf("logs appeared in the new working directory: %v", err)
	}
}

func TestRollingFileRotationsStayAnchoredAfterChdir(t *testing.T) {
	original, changed := t.TempDir(), t.TempDir()
	t.Chdir(original)
	clock := &fileTestClock{time: time.Now().UTC()}
	w, err := newRollingFile("stat.log", RotationConfig{MaxSizeMB: 1, UTC: true}, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	first, second := strings.Repeat("a", 600*1024), strings.Repeat("b", 600*1024)
	if _, err := w.Write([]byte(first)); err != nil {
		t.Fatal(err)
	}
	t.Chdir(changed)
	if _, err := w.Write([]byte(second)); err != nil {
		t.Fatalf("size rotation after chdir: %v", err)
	}
	clock.Advance(24 * time.Hour)
	if _, err := w.Write([]byte("next day\n")); err != nil {
		t.Fatalf("daily rotation after chdir: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(original, "stat.log")
	archives := logArchives(t, path)
	if len(archives) != 2 {
		t.Fatalf("archives = %v, want two", archives)
	}
	if got := readLogText(t, archives[0]) + readLogText(t, archives[1]); got != first+second && got != second+first {
		t.Fatal("size or daily rotation lost records")
	}
	if got := readLogText(t, path); got != "next day\n" {
		t.Fatalf("active log = %q", got)
	}
	entries, err := os.ReadDir(changed)
	if err != nil || len(entries) != 0 {
		t.Fatalf("new working directory contains logs: entries=%v err=%v", entries, err)
	}
}

func TestLoggerCloseStopsCleanupWorkers(t *testing.T) {
	for _, maxBackups := range []int{0, 1} {
		t.Run(fmt.Sprintf("max_backups=%d", maxBackups), func(t *testing.T) {
			before := logCleanupWorkerCount(t)
			dir := t.TempDir()
			for i := range 12 {
				logger, closeLog, err := NewLocalFirstLogger(Config{
					LocalPath: filepath.Join(dir, fmt.Sprintf("run-%d.log", i)),
					Rotation:  RotationConfig{MaxBackups: maxBackups},
				})
				if err != nil {
					t.Fatal(err)
				}
				logger.Info(context.Background(), "close worker")
				if err := closeLog(); err != nil {
					t.Fatal(err)
				}
			}
			// Completion may be signaled just before the worker returns from its
			// deferred call. Allow that stack to unwind before inspecting it.
			waitFileCondition(t, func() bool { return logCleanupWorkerCount(t) <= before })
		})
	}
}

func TestFailedStatInitStopsCleanupWorkers(t *testing.T) {
	path, _ := initStatTest(t, StatConfig{})
	previousLogger, previousStat := Default(), currentStat()
	before := logCleanupWorkerCount(t)
	dir, badStatPath := t.TempDir(), t.TempDir()
	for i := range 12 {
		closeLog, err := InitDefault(Config{
			LocalPath: filepath.Join(dir, fmt.Sprintf("retry-%d.log", i)),
			Rotation:  RotationConfig{MaxBackups: 1},
			Stat:      &StatConfig{LocalPath: badStatPath},
		})
		if err == nil {
			_ = closeLog()
			t.Fatal("invalid Stat path accepted")
		}
		if Default() != previousLogger || currentStat() != previousStat {
			t.Fatal("failed initialization replaced the active logger")
		}
	}
	waitFileCondition(t, func() bool { return logCleanupWorkerCount(t) <= before })
	if err := RecordStat(JobFinished{"still active", 1}, statTestBuild); err != nil {
		t.Fatal(err)
	}
	if rows := readStatRows(t, path); len(rows) != 1 || rows[0][5] != "still active" {
		t.Fatalf("previous logger records = %#v", rows)
	}
}

func logCleanupWorkerCount(t *testing.T) int {
	t.Helper()
	var profile bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&profile, 2); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, stack := range strings.Split(profile.String(), "\n\n") {
		// A newly created goroutine may still be in the go wrapper rather than
		// millRun. Include its creation frame, counting each stack only once.
		if strings.Contains(stack, "lumberjack") && strings.Contains(stack, ".(*Logger).mill") {
			count++
		}
	}
	return count
}
