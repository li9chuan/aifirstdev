package lumberjack

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCloseDrainsCompressionAndRetention(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stat.log")
	older := filepath.Join(dir, "stat-"+time.Now().UTC().Add(-2*time.Hour).Format(backupTimeFormat)+".log")
	newer := filepath.Join(dir, "stat-"+time.Now().UTC().Add(-time.Hour).Format(backupTimeFormat)+".log")
	data := bytes.Repeat([]byte("pending compression\n"), 65536)
	for _, archive := range []string{older, newer} {
		if err := os.WriteFile(archive, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	l := &Logger{Filename: path, MaxBackups: 1, Compress: true}
	t.Cleanup(func() { _ = l.Close() })
	if _, err := l.Write([]byte("active\n")); err != nil {
		t.Fatal(err)
	}
	// Close immediately, without polling the filesystem for the async work.
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	assertMillStopped(t, l)
	for _, removed := range []string{older, older + compressSuffix, newer} {
		if _, err := os.Stat(removed); !os.IsNotExist(err) {
			t.Fatalf("archive should be removed: %s, err=%v", removed, err)
		}
	}
	file, err := os.Open(newer + compressSuffix)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	r, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("closed before compression completed: bytes=%d err=%v", len(got), err)
	}
}

func TestConcurrentCloseAndWrite(t *testing.T) {
	l := &Logger{Filename: filepath.Join(t.TempDir(), "run.log"), MaxBackups: 1}
	t.Cleanup(func() { _ = l.Close() })
	if _, err := l.Write(nil); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 17)
	var writers sync.WaitGroup
	writers.Go(func() {
		<-start
		for {
			_, err := l.Write([]byte("concurrent write\n"))
			if errors.Is(err, os.ErrClosed) {
				results <- nil
				return
			}
			if err != nil {
				results <- err
				return
			}
		}
	})
	for range 16 {
		writers.Go(func() {
			<-start
			results <- l.Close()
		})
	}
	close(start)
	writers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertMillStopped(t, l)
	if _, err := l.Write(nil); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("write after close: %v", err)
	}
	if err := l.Rotate(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("rotate after close: %v", err)
	}
}

func TestCloseStopsCleanupAfterOpenFailure(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	l := &Logger{Filename: filepath.Join(blocker, "run.log"), MaxBackups: 1}
	t.Cleanup(func() { _ = l.Close() })
	if _, err := l.Write(nil); err == nil {
		t.Fatal("opening a file beneath a regular file succeeded")
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	assertMillStopped(t, l)
}

func TestCloseBeforeFirstWriteIsTerminal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "never-opened.log")
	l := &Logger{Filename: path}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Write(nil); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("write after close: %v", err)
	}
	if err := l.Rotate(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("rotate after close: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unused logger created a file: %v", err)
	}
}

func assertMillStopped(t *testing.T, l *Logger) {
	t.Helper()
	if l.millDone == nil {
		t.Fatal("test did not start a cleanup worker")
	}
	select {
	case <-l.millDone:
	default:
		t.Fatal("Close returned while cleanup was still running")
	}
}
