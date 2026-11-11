package logging

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	_ "time/tzdata"
)

type fileTestClock struct {
	mu      sync.Mutex
	time    time.Time
	started time.Time
}

func (c *fileTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started.IsZero() {
		return c.time.Add(time.Since(c.started))
	}
	return c.time
}

func (c *fileTestClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.time = c.time.Add(d)
}

func TestRollingFileDefaultLimitIsOneGiB(t *testing.T) {
	w, err := openLocalFile(filepath.Join(t.TempDir(), "stat.log"), RotationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.logger.MaxSize != 1024 || !w.daily {
		t.Fatalf("size=%d daily=%v", w.logger.MaxSize, w.daily)
	}
}

func TestRollingFileRestartChecksPreviousDayAndAppendsSameDay(t *testing.T) {
	for _, yesterday := range []bool{false, true} {
		t.Run(fmt.Sprint(yesterday), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "stat.log")
			now := time.Now().UTC()
			stamp := now
			if yesterday {
				stamp = stamp.AddDate(0, 0, -1)
			}
			if err := os.WriteFile(path, []byte("old\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			w, err := newRollingFile(path, RotationConfig{UTC: true}, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = w.Close() })
			if _, err := w.Write([]byte("new\n")); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			want := "old\nnew\n"
			archives := logArchives(t, path)
			if yesterday {
				want = "new\n"
				if len(archives) != 1 || readLogText(t, archives[0]) != "old\n" {
					t.Fatalf("archives=%v", archives)
				}
			} else if len(archives) != 0 {
				t.Fatalf("same-day restart rotated: %v", archives)
			}
			if got := readLogText(t, path); got != want {
				t.Fatalf("active=%q want=%q", got, want)
			}
		})
	}
}

func TestRollingFileChecksDateBeforeWritingAndSkipsEmptyDays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stat.log")
	clock := &fileTestClock{time: time.Now().UTC()}
	w, err := newRollingFile(path, RotationConfig{UTC: true}, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Write([]byte("before\n")); err != nil {
		t.Fatal(err)
	}
	clock.Advance(24 * time.Hour)
	if _, err := w.Write([]byte("after\n")); err != nil {
		t.Fatal(err)
	}
	archives := logArchives(t, path)
	if len(archives) != 1 || readLogText(t, archives[0]) != "before\n" || readLogText(t, path) != "after\n" {
		t.Fatalf("archives=%v active=%q", archives, readLogText(t, path))
	}
	clock.Advance(24 * time.Hour)
	if err := w.rotateDay(); err != nil {
		t.Fatal(err)
	}
	count := len(logArchives(t, path))
	if count != 2 {
		t.Fatalf("successive date rotations lost an archive: %d", count)
	}
	clock.Advance(72 * time.Hour)
	if err := w.rotateDay(); err != nil {
		t.Fatal(err)
	}
	if got := len(logArchives(t, path)); got != count {
		t.Fatalf("empty days created archives: %d -> %d", count, got)
	}
}

func TestDailyWorkerRotatesWithoutAnotherWrite(t *testing.T) {
	clock := &fileTestClock{time: nextMidnight(time.Now().UTC()).Add(-100 * time.Millisecond)}
	path := filepath.Join(t.TempDir(), "service.log")
	w, err := newRollingFile(path, RotationConfig{UTC: true}, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Write([]byte("idle at midnight\n")); err != nil {
		t.Fatal(err)
	}
	clock.mu.Lock()
	clock.started = time.Now()
	clock.mu.Unlock()
	waitFileCondition(t, func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.day.After(startOfDay(clock.time))
	})
	if got := readLogText(t, path); got != "" {
		t.Fatalf("idle active=%q", got)
	}
	archives := logArchives(t, path)
	if len(archives) != 1 || readLogText(t, archives[0]) != "idle at midnight\n" {
		t.Fatalf("archives=%v", archives)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.done:
	default:
		t.Fatal("daily worker did not stop")
	}
}

func TestDailyFailureKeepsDayPendingAndRetriesBeforeAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stat.log")
	clock := &fileTestClock{time: time.Now().UTC()}
	w, err := newRollingFile(path, RotationConfig{UTC: true}, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Write([]byte("old\n")); err != nil {
		t.Fatal(err)
	}
	clock.Advance(24 * time.Hour)
	w.mu.Lock()
	day := w.day
	w.logger.Filename = filepath.Join(path, "not-a-directory.log")
	err = w.rotateDayUnsafe(clock.Now())
	stillPending := w.day.Equal(day)
	w.logger.Filename = path
	w.mu.Unlock()
	if err == nil || !stillPending {
		t.Fatalf("err=%v pending=%v", err, stillPending)
	}
	if _, err := w.Write([]byte("new\n")); err != nil {
		t.Fatal(err)
	}
	if got := readLogText(t, path); got != "new\n" {
		t.Fatalf("failed day was appended: %q", got)
	}
}

func TestRollingSizeLimitRejectsOversizedRecordWithoutPartialWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stat.log")
	w, err := openLocalFile(path, RotationConfig{MaxSizeMB: 1, DisableDaily: true})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	first, second := strings.Repeat("a", 600*1024)+"\n", strings.Repeat("b", 600*1024)+"\n"
	if _, err := w.Write([]byte(first)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(second)); err != nil {
		t.Fatal(err)
	}
	if n, err := w.Write(make([]byte, 1024*1024+1)); err == nil || n != 0 {
		t.Fatalf("oversize n=%d err=%v", n, err)
	}
	archives := logArchives(t, path)
	if len(archives) != 1 || readLogText(t, archives[0]) != first || readLogText(t, path) != second {
		t.Fatalf("archives=%v", archives)
	}
}

func TestRapidSizeAndDateRotationsNeverOverwriteArchives(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stat.log")
	clock := &fileTestClock{time: time.Now().UTC()}
	w, err := newRollingFile(path, RotationConfig{MaxSizeMB: 1, UTC: true}, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for i := 0; i < 6; i++ {
		if i == 4 {
			clock.Advance(24 * time.Hour)
		}
		record := fmt.Sprintf("%d,%s\n", i, strings.Repeat("x", 600*1024))
		if _, err := w.Write([]byte(record)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	files := append(logArchives(t, path), path)
	if len(files) != 6 {
		t.Fatalf("files=%d, want 6 complete records", len(files))
	}
	seen := make(map[byte]bool)
	for _, file := range files {
		text := readLogText(t, file)
		if len(text) == 0 || strings.Count(text, "\n") != 1 || seen[text[0]] {
			t.Fatalf("lost or duplicated record in %s", file)
		}
		seen[text[0]] = true
	}
}

func TestRollingFileConcurrentWriteDateCheckAndClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stat.log")
	clock := &fileTestClock{time: time.Now().UTC()}
	w, err := newRollingFile(path, RotationConfig{UTC: true}, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	var accepted atomic.Int64
	var workers sync.WaitGroup
	ready := make(chan struct{}, 8)
	for i := 0; i < 8; i++ {
		workers.Go(func() {
			for j := 0; j < 200; j++ {
				_, err := w.Write([]byte(fmt.Sprintf("%d,%d\n", i, j)))
				if err == nil {
					accepted.Add(1)
				} else if !errors.Is(err, os.ErrClosed) {
					t.Error(err)
				}
				if j == 0 {
					ready <- struct{}{}
				}
			}
		})
	}
	for i := 0; i < 8; i++ {
		<-ready
	}
	clock.Advance(24 * time.Hour)
	if err := w.rotateDay(); err != nil {
		t.Fatal(err)
	}
	var closers sync.WaitGroup
	for i := 0; i < 2; i++ {
		closers.Go(func() {
			if err := w.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	closers.Wait()
	workers.Wait()
	if _, err := w.Write([]byte("late\n")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("write after close=%v", err)
	}
	var count int
	for _, file := range append(logArchives(t, path), path) {
		count += strings.Count(readLogText(t, file), "\n")
	}
	if int64(count) != accepted.Load() {
		t.Fatalf("rows=%d accepted=%d", count, accepted.Load())
	}
}

func TestLumberjackRetentionAndCompressionAreWired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	now := time.Now().UTC()
	var newest string
	for i, age := range []time.Duration{time.Hour, 2 * time.Hour, 72 * time.Hour} {
		name := filepath.Join(filepath.Dir(path), "service-"+now.Add(-age).Format("2006-01-02T15-04-05.000")+".log")
		if err := os.WriteFile(name, []byte("archived\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			newest = name
		}
	}
	w, err := openLocalFile(path, RotationConfig{MaxBackups: 1, MaxAgeDays: 1, Compress: true, UTC: true})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	waitFileCondition(t, func() bool {
		archives := logArchives(t, path)
		return len(archives) == 1 && archives[0] == newest+".gz"
	})
	f, err := os.Open(newest + ".gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	raw, err := io.ReadAll(zr)
	if err != nil || string(raw) != "archived\n" {
		t.Fatalf("gzip=%q err=%v", raw, err)
	}
}

func TestNextMidnightUsesCalendarDaysAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		month    time.Month
		day      int
		duration time.Duration
	}{
		{time.March, 8, 23 * time.Hour}, {time.November, 1, 25 * time.Hour},
	} {
		now := time.Date(2026, tc.month, tc.day, 0, 0, 0, 0, loc)
		if got := nextMidnight(now).Sub(now); got != tc.duration {
			t.Fatalf("%s day length=%s want=%s", now, got, tc.duration)
		}
	}
}

func logArchives(t *testing.T, path string) []string {
	t.Helper()
	ext := filepath.Ext(path)
	files, err := filepath.Glob(strings.TrimSuffix(path, ext) + "-*" + ext + "*")
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func waitFileCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("file condition timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
