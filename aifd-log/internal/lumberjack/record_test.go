package lumberjack

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordWritesRollBackFailedAppends(t *testing.T) {
	for _, source := range []string{"fresh", "preexisting", "reopened"} {
		for _, failure := range []string{"partial error", "short nil error", "full error", "after rotation"} {
			t.Run(source+"/"+failure, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "stat.log")
				if source == "preexisting" {
					if err := os.WriteFile(path, []byte("old,1\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				l := &Logger{Filename: path, MaxSize: 1, RecordWrites: true}
				t.Cleanup(func() { _ = l.Close() })
				original := writeFile
				originalMegabyte := megabyte
				t.Cleanup(func() { writeFile, megabyte = original, originalMegabyte })
				if failure == "after rotation" {
					megabyte = 32
				}
				first := []byte("old,1\n")
				if source == "preexisting" {
					first = nil // Match the eager open used by the local Stat writer.
				}
				if _, err := l.Write(first); err != nil {
					t.Fatal(err)
				}
				if source == "reopened" {
					if err := l.Close(); err != nil {
						t.Fatal(err)
					}
					l = &Logger{Filename: path, MaxSize: 1, RecordWrites: true}
					if _, err := l.Write(nil); err != nil {
						t.Fatal(err)
					}
				}
				bad := "bad," + strings.Repeat("x", 22) + "\n"
				sentinel := errors.New("injected disk full")
				writeFile = func(file *os.File, p []byte) (int, error) {
					if string(p) != bad {
						return original(file, p)
					}
					length := 5
					if failure == "full error" {
						length = len(p)
					}
					n, err := original(file, p[:length])
					if err != nil {
						return n, err
					}
					if failure == "short nil error" {
						return n, nil
					}
					return n, sentinel
				}
				wantError := sentinel
				if failure == "short nil error" {
					wantError = io.ErrShortWrite
				}
				if n, err := l.Write([]byte(bad)); n != 0 || !errors.Is(err, wantError) {
					t.Fatalf("unaccepted record n=%d err=%v", n, err)
				}
				if _, err := l.Write([]byte("new,2\n")); err != nil {
					t.Fatal(err)
				}
				if err := l.Close(); err != nil {
					t.Fatal(err)
				}
				want := "old,1\nnew,2\n"
				if failure == "after rotation" {
					want = "new,2\n"
					archives, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "stat-*.log"))
					if len(archives) != 1 {
						t.Fatalf("rotation archives=%v", archives)
					}
					if raw, err := os.ReadFile(archives[0]); err != nil || string(raw) != "old,1\n" {
						t.Fatalf("archived prefix=%q err=%v", raw, err)
					}
				}
				if raw, err := os.ReadFile(path); err != nil || string(raw) != want {
					t.Fatalf("partial append was not rolled back: %q err=%v", raw, err)
				}
			})
		}
	}
}

func TestRecordReopenFailurePreservesExistingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stat.log")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(path, "original")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	l := &Logger{Filename: path, RecordWrites: true}
	t.Cleanup(func() { _ = l.Close() })
	n, err := l.Write([]byte("new,2\n"))
	var pathError *os.PathError
	if n != 0 || !errors.As(err, &pathError) || pathError.Op != "open" || pathError.Path != path {
		t.Fatalf("reopen failure n=%d err=%v", n, err)
	}
	if l.file != nil {
		t.Fatal("failed reopen published a file handle")
	}
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "keep" {
		t.Fatalf("original path changed after reopen failure: %q err=%v", raw, err)
	}
	archives, err := filepath.Glob(filepath.Join(filepath.Dir(path), "stat-*.log"))
	if err != nil || len(archives) != 0 {
		t.Fatalf("failed reopen created archives=%v err=%v", archives, err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordRollbackFailureRejectsWritesAndRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stat.log")
	l := &Logger{Filename: path, RecordWrites: true}
	t.Cleanup(func() { _ = l.Close() })
	if _, err := l.Write([]byte("old,1\n")); err != nil {
		t.Fatal(err)
	}
	original := writeFile
	t.Cleanup(func() { writeFile = original })
	sentinel := errors.New("injected partial append")
	writeFile = func(file *os.File, p []byte) (int, error) {
		n, err := original(file, p[:3])
		if err != nil {
			return n, err
		}
		_ = file.Close() // Force Truncate/Seek to fail after bytes reached disk.
		return n, sentinel
	}
	if n, err := l.Write([]byte("bad,2\n")); n != 0 || !errors.Is(err, sentinel) {
		t.Fatalf("partial append n=%d err=%v", n, err)
	}
	if _, err := l.Write([]byte("must not append\n")); !errors.Is(err, sentinel) {
		t.Fatalf("poisoned file accepted another record: %v", err)
	}
	if err := l.Rotate(); !errors.Is(err, sentinel) {
		t.Fatalf("poisoned file rotated without recovery: %v", err)
	}
	if err := l.Close(); !errors.Is(err, sentinel) {
		t.Fatalf("Close lost the unrecovered append error: %v", err)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "old,1\nbad" {
		t.Fatalf("unrecovered file changed: %q err=%v", raw, err)
	}
}
