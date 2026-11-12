package logging

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-log/internal/lumberjack"
)

// RotationConfig applies independently to each ordinary or Stat file. Zero values
// mean 1 GiB, local calendar-day rotation, no compression and unlimited backups.
// Retention limits count archives, not records. Each path must have one owner.
type RotationConfig struct {
	MaxSizeMB    int  `mapstructure:"max_size_mb"`
	MaxBackups   int  `mapstructure:"max_backups"`
	MaxAgeDays   int  `mapstructure:"max_age_days"`
	Compress     bool `mapstructure:"compress"`
	DisableDaily bool `mapstructure:"disable_daily"`
	UTC          bool `mapstructure:"utc"`
}

func (c RotationConfig) validate() error {
	if c.MaxSizeMB < 0 || c.MaxBackups < 0 || c.MaxAgeDays < 0 {
		return fmt.Errorf("log rotation sizes and retention cannot be negative")
	}
	return nil
}

// rollingFile leaves size rotation, archive naming, retention and compression to
// Lumberjack. This lock also serializes date checks and writes with Close. The
// internal Lumberjack copy also stops its cleanup worker when closed.
type rollingFile struct {
	mu       sync.Mutex
	logger   *lumberjack.Logger
	now      func() time.Time
	location *time.Location
	day      time.Time
	hasData  bool
	size     int64
	maxSize  int64
	daily    bool
	closed   bool
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
	closeErr error
}

func openLocalFile(path string, config RotationConfig) (*rollingFile, error) {
	return newRollingFile(path, config, time.Now)
}

func openStatFile(path string, config RotationConfig) (*rollingFile, error) {
	return newFile(path, config, time.Now, true)
}

func newRollingFile(path string, config RotationConfig, now func() time.Time) (*rollingFile, error) {
	return newFile(path, config, now, false)
}

func newFile(path string, config RotationConfig, now func() time.Time, csvRecords bool) (*rollingFile, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	// Bind relative paths before any file operations so later Chdir calls cannot
	// redirect rotation, cleanup or the final Sync to a different directory.
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve local log path: %w", err)
	}
	if config.MaxSizeMB == 0 {
		config.MaxSizeMB = 1024
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create local log directory: %w", err)
	}
	if csvRecords {
		if err := recoverStatCSV(path); err != nil {
			return nil, err
		}
	}
	location := time.Local
	if config.UTC {
		location = time.UTC
	}
	w := &rollingFile{
		logger: &lumberjack.Logger{
			Filename: path, MaxSize: config.MaxSizeMB, MaxBackups: config.MaxBackups,
			MaxAge: config.MaxAgeDays, Compress: config.Compress, LocalTime: !config.UTC,
			RecordWrites: csvRecords, OnError: reportLocalError,
		},
		now: now, location: location, daily: !config.DisableDaily,
		maxSize: int64(config.MaxSizeMB) * 1024 * 1024,
		stop:    make(chan struct{}), done: make(chan struct{}),
	}
	w.day = startOfDay(now().In(location))
	// Inspect before opening: a process that was stopped over midnight must not
	// append today's first record to yesterday's nonempty active file.
	info, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat local log file: %w", err)
	}
	if err == nil && info.IsDir() {
		return nil, fmt.Errorf("local log path is a directory: %s", path)
	}
	if err == nil && w.daily && info.Size() > 0 && startOfDay(info.ModTime().In(location)).Before(w.day) {
		if err := w.waitArchiveName(); err != nil {
			return nil, err
		}
		if err := w.logger.Rotate(); err != nil {
			_ = w.logger.Close()
			return nil, fmt.Errorf("rotate previous-day log: %w", err)
		}
	}
	// Eager open surfaces permission/path errors at initialization. A zero-byte
	// write adds no record and lets Lumberjack check an oversized existing file.
	if err == nil && info.Size() >= w.maxSize {
		if err := w.waitArchiveName(); err != nil {
			_ = w.logger.Close()
			return nil, err
		}
	}
	if _, err := w.logger.Write(nil); err != nil {
		_ = w.logger.Close()
		return nil, fmt.Errorf("open local log file: %w", err)
	}
	info, err = os.Stat(path)
	if err != nil {
		_ = w.logger.Close()
		return nil, err
	}
	w.hasData = info.Size() > 0
	w.size = info.Size()
	if w.daily {
		go w.runDaily()
	} else {
		close(w.done)
	}
	return w, nil
}

func (w *rollingFile) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	if err := w.rotateDayUnsafe(w.now()); err != nil {
		return 0, err
	}
	// Lumberjack still performs the size rotation. Track accepted bytes only to
	// guard its millisecond archive name before a write that could rotate.
	rotates := w.size+int64(len(p)) > w.maxSize
	if rotates && int64(len(p)) <= w.maxSize {
		if err := w.waitArchiveName(); err != nil {
			return 0, err
		}
	}
	n, err := w.logger.Write(p)
	if err != nil {
		// A size rotation may already have succeeded before an append failed.
		// Refresh only on failures, including a rolled-back partial CSV append.
		if info, statErr := os.Stat(w.logger.Filename); statErr == nil {
			w.size, w.hasData = info.Size(), info.Size() > 0
		}
		return n, err
	}
	if n > 0 {
		w.hasData = true
		if rotates {
			w.size = int64(n)
		} else {
			w.size += int64(n)
		}
	}
	return n, err
}

// rotateDayUnsafe is shared by the timer and the pre-write check. Failed rotation
// leaves the day pending for retry; a backwards clock jump does not rotate again.
func (w *rollingFile) rotateDayUnsafe(now time.Time) error {
	day := startOfDay(now.In(w.location))
	if !w.daily || !day.After(w.day) {
		return nil
	}
	if w.hasData {
		if err := w.waitArchiveName(); err != nil {
			return err
		}
		if err := w.logger.Rotate(); err != nil {
			return fmt.Errorf("rotate daily log: %w", err)
		}
		w.hasData = false
		w.size = 0
	}
	w.day = day
	return nil
}

// waitArchiveName avoids silently replacing a backup when multiple rotations
// land in one millisecond, including a size rotation adjacent to midnight.
// Only rotations do these filesystem checks; ordinary appends do not stat.
func (w *rollingFile) waitArchiveName() error {
	for {
		name := lumberjack.BackupName(w.logger.Filename, w.logger.LocalTime)
		occupied := false
		for _, candidate := range []string{name, name + ".gz"} {
			if _, err := os.Stat(candidate); err == nil {
				occupied = true
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("check log archive name: %w", err)
			}
		}
		if !occupied {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
}

func (w *rollingFile) rotateDay() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	return w.rotateDayUnsafe(w.now())
}

func (w *rollingFile) runDaily() {
	defer close(w.done)
	// Check once immediately as well: the goroutine may first be scheduled
	// after a midnight crossed during initialization.
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-timer.C:
			err := w.rotateDay()
			delay := w.untilMidnight()
			if err != nil {
				reportLocalError(err)
				if delay > time.Minute {
					delay = time.Minute
				}
			}
			timer.Reset(delay)
		}
	}
}

func (w *rollingFile) untilMidnight() time.Duration {
	now := w.now().In(w.location)
	return nextMidnight(now).Sub(now)
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func nextMidnight(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
}

func (w *rollingFile) Close() error {
	w.once.Do(func() {
		w.mu.Lock()
		w.closed = true
		close(w.stop)
		// No userspace buffering exists here. Sync the active file on graceful
		// close, then always close Lumberjack even if Sync fails.
		file, err := os.OpenFile(w.logger.Filename, os.O_WRONLY, 0)
		if err == nil {
			err = errors.Join(file.Sync(), file.Close())
		}
		w.closeErr = errors.Join(err, w.logger.Close())
		w.mu.Unlock()
		<-w.done
	})
	return w.closeErr
}

func reportLocalError(err error) {
	// Ordinary slog helpers have no error result. Keep local failures visible
	// without recursively trying to write to the failed logging destination.
	_, _ = fmt.Fprintf(os.Stderr, "aifd-log: %v\n", err)
}
