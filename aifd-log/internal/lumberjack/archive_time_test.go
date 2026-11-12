package lumberjack

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const archiveOffsetLayout = "2006-01-02T15-04-05.000-0700"
const archiveLegacyLayout = "2006-01-02T15-04-05.000"

type archiveFold struct {
	name        string
	early, late time.Time
}

func archiveFolds(t *testing.T) []archiveFold {
	t.Helper()
	var folds []archiveFold
	for _, fixture := range []struct {
		zone, early, late string
	}{
		{"America/New_York", "2026-11-01T05:30:00.123Z", "2026-11-01T06:30:00.123Z"},
		{"Australia/Lord_Howe", "2026-04-04T14:45:00.123Z", "2026-04-04T15:15:00.123Z"},
	} {
		zone, err := time.LoadLocation(fixture.zone)
		if err != nil {
			t.Fatal(err)
		}
		early, err := time.Parse(time.RFC3339Nano, fixture.early)
		if err != nil {
			t.Fatal(err)
		}
		late, err := time.Parse(time.RFC3339Nano, fixture.late)
		if err != nil {
			t.Fatal(err)
		}
		early, late = early.In(zone), late.In(zone)
		if early.Format(archiveLegacyLayout) != late.Format(archiveLegacyLayout) {
			t.Fatalf("fixture does not repeat a wall time: %s %s", early, late)
		}
		folds = append(folds, archiveFold{fixture.zone, early, late})
	}
	return folds
}

func TestArchiveNameIncludesOffset(t *testing.T) {
	for _, tc := range []struct {
		name, instant, want string
		local               bool
	}{
		{"EDT", "2026-11-01T01:30:00.123-04:00", "stat-2026-11-01T01-30-00.123-0400.log", true},
		{"EST", "2026-11-01T01:30:00.123-05:00", "stat-2026-11-01T01-30-00.123-0500.log", true},
		{"UTC+8", "2026-11-12T00:00:00.123+08:00", "stat-2026-11-12T00-00-00.123+0800.log", true},
		{"UTC-8", "2026-11-12T00:00:00.123-08:00", "stat-2026-11-12T00-00-00.123-0800.log", true},
		{"UTC", "2026-11-12T00:00:00.123+08:00", "stat-2026-11-11T16-00-00.123+0000.log", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			instant, err := time.Parse(time.RFC3339Nano, tc.instant)
			if err != nil {
				t.Fatal(err)
			}
			previous := currentTime
			currentTime = func() time.Time { return instant }
			t.Cleanup(func() { currentTime = previous })
			path := filepath.Join(t.TempDir(), "stat.log")
			if name := BackupName(path, tc.local); name != filepath.Join(filepath.Dir(path), tc.want) {
				t.Fatalf("archive name=%s want=%s", name, tc.want)
			}
			// An explicit offset must be authoritative even after a config change.
			for _, local := range []bool{false, true} {
				l := &Logger{LocalTime: local}
				got, err := l.timeFromName(tc.want, "stat-", ".log")
				if err != nil || !got.Equal(instant) {
					t.Fatalf("local=%t: parsed=%s want=%s err=%v", local, got, instant, err)
				}
			}
		})
	}
}

func TestLegacyArchiveTimeUsesLatestFold(t *testing.T) {
	for _, fold := range archiveFolds(t) {
		t.Run(fold.name, func(t *testing.T) {
			previous := currentTime
			currentTime = func() time.Time { return fold.late.AddDate(0, 0, 1) }
			t.Cleanup(func() { currentTime = previous })
			l := &Logger{LocalTime: true}
			for _, instant := range []time.Time{fold.early.Add(-2 * time.Hour), fold.early, fold.late, fold.late.Add(2 * time.Hour)} {
				want := instant
				if instant.Equal(fold.early) {
					want = fold.late
				}
				name := "stat-" + instant.Format(archiveLegacyLayout) + ".log.gz"
				got, err := l.timeFromName(name, "stat-", ".log.gz")
				if err != nil || !got.Equal(want) {
					t.Fatalf("%s: parsed=%s want=%s err=%v", name, got, want, err)
				}
			}
		})
	}
}

func TestArchiveRetentionAcrossFold(t *testing.T) {
	for _, fold := range archiveFolds(t) {
		for _, elapsed := range []time.Duration{24*time.Hour - 15*time.Minute, 24 * time.Hour, 24*time.Hour + time.Millisecond} {
			t.Run(fmt.Sprintf("%s/%s", fold.name, elapsed), func(t *testing.T) {
				now := fold.late.Add(elapsed)
				previous := currentTime
				currentTime = func() time.Time { return now }
				t.Cleanup(func() { currentTime = previous })
				l := &Logger{Filename: filepath.Join(t.TempDir(), "stat.log"), MaxAge: 1, LocalTime: true}
				files := map[string]time.Time{
					fold.early.Format(archiveOffsetLayout) + ".log":   fold.early,
					fold.late.Format(archiveOffsetLayout) + ".log.gz": fold.late,
					fold.late.Format(archiveLegacyLayout) + ".log":    fold.late,
					fold.late.Format(archiveLegacyLayout) + ".log.gz": fold.late,
				}
				for suffix := range files {
					if err := os.WriteFile(filepath.Join(l.dir(), "stat-"+suffix), []byte("archive\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := l.millRunOnce(); err != nil {
					t.Fatal(err)
				}
				for suffix, instant := range files {
					wantPresent := !instant.Before(now.Add(-24 * time.Hour))
					_, err := os.Stat(filepath.Join(l.dir(), "stat-"+suffix))
					if (err == nil) != wantPresent || err != nil && !os.IsNotExist(err) {
						t.Errorf("%s: exists=%t want=%t err=%v", suffix, err == nil, wantPresent, err)
					}
				}
			})
		}
	}
}

func TestArchiveOrderingAcrossFoldAndFormats(t *testing.T) {
	fold := archiveFolds(t)[0]
	previous := currentTime
	currentTime = func() time.Time { return fold.late.AddDate(0, 0, 1) }
	t.Cleanup(func() { currentTime = previous })
	l := &Logger{Filename: filepath.Join(t.TempDir(), "stat.log"), MaxBackups: 1, LocalTime: true}
	// Actual order differs from wall-clock order: 01:30 EST, 01:10 EST, 01:50 EDT.
	names := []string{
		"stat-" + fold.late.Format(archiveLegacyLayout) + ".log.gz",
		"stat-" + fold.late.Add(-20*time.Minute).Format(archiveOffsetLayout) + ".log",
		"stat-" + fold.early.Add(20*time.Minute).Format(archiveOffsetLayout) + ".log.gz",
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(l.dir(), name), []byte("archive\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := l.oldLogFiles()
	if err != nil || len(files) != len(names) {
		t.Fatalf("archives=%v err=%v", files, err)
	}
	for i, file := range files {
		if file.Name() != names[i] {
			t.Fatalf("archive %d=%s want=%s", i, file.Name(), names[i])
		}
	}
	if err := l.millRunOnce(); err != nil {
		t.Fatal(err)
	}
	for i, name := range names {
		_, err := os.Stat(filepath.Join(l.dir(), name))
		if (err == nil) != (i == 0) || err != nil && !os.IsNotExist(err) {
			t.Errorf("MaxBackups kept wrong archive: %s err=%v", name, err)
		}
	}
}

func TestRotationAcrossFoldKeepsBothArchives(t *testing.T) {
	for _, fold := range archiveFolds(t) {
		t.Run(fold.name, func(t *testing.T) {
			instant := fold.early
			previous := currentTime
			currentTime = func() time.Time { return instant }
			t.Cleanup(func() { currentTime = previous })
			l := &Logger{Filename: filepath.Join(t.TempDir(), "stat.log"), LocalTime: true, RecordWrites: true}
			t.Cleanup(func() { _ = l.Close() })
			for i, stamp := range []time.Time{fold.early, fold.late} {
				instant = stamp
				if _, err := l.Write([]byte(fmt.Sprintf("record,%d\n", i))); err != nil {
					t.Fatal(err)
				}
				if err := l.Rotate(); err != nil {
					t.Fatal(err)
				}
			}
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
			for i, stamp := range []time.Time{fold.early, fold.late} {
				name := filepath.Join(l.dir(), "stat-"+stamp.Format(archiveOffsetLayout)+".log")
				raw, err := os.ReadFile(name)
				if err != nil || string(raw) != fmt.Sprintf("record,%d\n", i) {
					t.Fatalf("archive %s: %q err=%v", name, raw, err)
				}
			}
		})
	}
}
