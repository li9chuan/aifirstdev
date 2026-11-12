package lumberjack

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	_ "time/tzdata"
)

func TestRetentionUsesArchiveTimeZone(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, zone := range []*time.Location{time.UTC, time.FixedZone("UTC-8", -8*3600), time.FixedZone("UTC+8", 8*3600), ny} {
		for _, local := range []bool{false, true} {
			for _, layout := range []string{backupTimeFormat, legacyBackupTimeFormat} {
				t.Run(fmt.Sprintf("%s/local=%t/layout=%s", zone, local, layout), func(t *testing.T) {
					now := time.Date(2026, 3, 8, 12, 0, 0, 0, zone)
					previous := currentTime
					currentTime = func() time.Time { return now }
					t.Cleanup(func() { currentTime = previous })
					l := &Logger{Filename: filepath.Join(t.TempDir(), "stat.log"), MaxAge: 1, LocalTime: local}
					archives := make(map[string]bool)
					for i, age := range []time.Duration{20 * time.Hour, 23*time.Hour + 30*time.Minute, 24*time.Hour + 30*time.Minute, 28 * time.Hour} {
						stamp := now.Add(-age)
						if !local {
							stamp = stamp.UTC()
						}
						name := filepath.Join(l.dir(), "stat-"+stamp.Format(layout)+".log")
						if i%2 == 0 {
							name += compressSuffix
						}
						if err := os.WriteFile(name, []byte("archive\n"), 0600); err != nil {
							t.Fatal(err)
						}
						archives[name] = age < 24*time.Hour
					}
					if err := l.millRunOnce(); err != nil {
						t.Fatal(err)
					}
					for name, wantPresent := range archives {
						_, err := os.Stat(name)
						if (err == nil) != wantPresent || err != nil && !os.IsNotExist(err) {
							t.Errorf("archive %s: exists=%t want=%t err=%v", filepath.Base(name), err == nil, wantPresent, err)
						}
					}
				})
			}
		}
	}
}

func TestCloseReportsBackgroundCompressionFailure(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "stat-"+time.Now().UTC().Add(-time.Hour).Format(backupTimeFormat)+".log")
	if err := os.WriteFile(archive, []byte("preserve this archive\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(archive+compressSuffix, 0700); err != nil {
		t.Fatal(err)
	}
	reported := make(chan error, 4)
	l := &Logger{Filename: filepath.Join(dir, "stat.log"), Compress: true, OnError: func(err error) { reported <- err }}
	t.Cleanup(func() { _ = l.Close() })
	if _, err := l.Write(nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-reported:
		if !strings.Contains(err.Error(), archive) {
			t.Fatalf("diagnostic omitted failing archive: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("background failure was not reported before Close")
	}
	first := l.Close()
	if first == nil || !strings.Contains(first.Error(), "compressed log file") {
		t.Fatalf("background compression error lost: %v", first)
	}
	if err := l.Close(); err != first {
		t.Fatalf("background compression error lost: %v", err)
	}
	assertMillStopped(t, l)
	if raw, err := os.ReadFile(archive); err != nil || string(raw) != "preserve this archive\n" {
		t.Fatalf("failed compression damaged the source: %q %v", raw, err)
	}
}

func TestCleanupFailureIsReportedAndRetainedAfterRetry(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "stat-"+time.Now().UTC().Add(-48*time.Hour).Format(backupTimeFormat)+".log")
	if err := os.WriteFile(archive, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	previous := removeFile
	var blocked atomic.Bool
	blocked.Store(true)
	removeFile = func(path string) error {
		if path == archive && blocked.Load() {
			return fmt.Errorf("delete archive: %w", os.ErrPermission)
		}
		return previous(path)
	}
	t.Cleanup(func() { removeFile = previous })
	reported := make(chan error, 2)
	l := &Logger{Filename: filepath.Join(filepath.Dir(archive), "stat.log"), MaxAge: 1, OnError: func(err error) { reported <- err }}
	t.Cleanup(func() { _ = l.Close() })
	if _, err := l.Write(nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-reported:
		if !errors.Is(err, os.ErrPermission) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("deletion failure was not reported")
	}
	// Acknowledge the first sweep before changing the failure condition.
	// The worker is idle after reporting; the next mill notification retries.
	blocked.Store(false)
	if err := l.Rotate(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("retry erased the previously reported error: %v", err)
	}
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatalf("cleanup retry did not remove archive: %v", err)
	}
}
