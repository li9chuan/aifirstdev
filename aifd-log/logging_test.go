package logging

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewLocalFirstLoggerRejectsUnopenableLocalPath(t *testing.T) {
	logger, closeFn, err := NewLocalFirstLogger(Config{LocalPath: t.TempDir()})
	if err == nil {
		_ = closeFn()
		t.Fatalf("NewLocalFirstLogger logger=%v err=nil, want error", logger)
	}
}

func TestInitDefaultWritesLocalTextFile(t *testing.T) {
	previous := Default()
	t.Cleanup(func() { SetDefault(previous) })
	path := filepath.Join(t.TempDir(), "service.log")
	closeFn, err := InitDefault(Config{
		LocalPath: path, SourceService: "minigame", SourceInstance: "inst-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeFn() })
	Info("service started", String("stage", "start"))
	if err := closeFn(); err != nil {
		t.Fatal(err)
	}
	text := readLogText(t, path)
	for _, want := range []string{"time=", "level=INFO", `msg="service started"`, "source_service=minigame", "source_instance=inst-1", "stage=start"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %q", want, text)
		}
	}
	if strings.HasPrefix(text, "{") {
		t.Fatalf("ordinary log is JSON: %s", text)
	}
}

func TestSecondaryFailureDoesNotBlockLocalWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	secondary := slog.NewTextHandler(writeFunc(func([]byte) (int, error) { return 0, errors.New("secondary failed") }), nil)
	logger, closeFn, err := NewLocalFirstLogger(Config{LocalPath: path, SecondaryHandler: secondary})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeFn() })
	logger.Info(context.Background(), "local survives", String("stage", "copy"))
	if err := closeFn(); err != nil {
		t.Fatal(err)
	}
	text := readLogText(t, path)
	if !strings.Contains(text, `msg="local survives"`) || !strings.Contains(text, "stage=copy") {
		t.Fatal(text)
	}
}

func TestPackageHelpersWriteWithoutContextAndEscapeNewlines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	logger, closeFn, err := NewLocalFirstLogger(Config{LocalPath: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeFn() })
	previous := Default()
	t.Cleanup(func() { SetDefault(previous) })
	SetDefault(logger)
	Info("service\nstarted", String("stage", "start"))
	Warnf("retry %s", "save")
	if err := closeFn(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(readLogText(t, path)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `msg="service\nstarted"`) || !strings.Contains(lines[1], `level=WARN msg="retry save"`) {
		t.Fatalf("text records = %#v", lines)
	}
}

func TestLoggerFieldsModuleAndLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	logger, closeFn, err := NewLocalFirstLogger(Config{LocalPath: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeFn() })
	logger.Debug(context.Background(), "hidden debug")
	logger.Module("rdb").With(String("stage", "flush")).Error(context.Background(), "flush failed",
		Int64("uid", 1001), Bool("retry", true), Duration("elapsed", 3*time.Second), Err(errors.New("redis down")))
	if err := closeFn(); err != nil {
		t.Fatal(err)
	}
	text := readLogText(t, path)
	for _, want := range []string{"level=ERROR", "module=rdb", "stage=flush", "uid=1001", "retry=true", "elapsed=3s", `err="redis down"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %q", want, text)
		}
	}
	if strings.Contains(text, "hidden debug") {
		t.Fatal(text)
	}
}

func TestTeePreservesOrderGroupsAndLocalFailure(t *testing.T) {
	var order []string
	var local, secondary bytes.Buffer
	handler, err := NewTeeHandler(
		slog.NewTextHandler(writeFunc(func(p []byte) (int, error) { order = append(order, "local"); return local.Write(p) }), nil),
		slog.NewTextHandler(writeFunc(func(p []byte) (int, error) { order = append(order, "secondary"); return secondary.Write(p) }), nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(handler).With("module", "jobs").WithGroup("request").With("uid", 1001)
	logger.Info("finished", "result", "ok")
	if strings.Join(order, ",") != "local,secondary" || local.String() != secondary.String() || !strings.Contains(local.String(), "request.uid=1001") || !strings.Contains(local.String(), "request.result=ok") {
		t.Fatalf("order=%v local=%s secondary=%s", order, &local, &secondary)
	}
	failure := errors.New("disk failed")
	handler, err = NewTeeHandler(slog.NewTextHandler(writeFunc(func([]byte) (int, error) { return 0, failure }), nil), slog.NewTextHandler(&secondary, nil))
	if err != nil {
		t.Fatal(err)
	}
	size := secondary.Len()
	if err := handler.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "must not copy", 0)); !errors.Is(err, failure) || secondary.Len() != size {
		t.Fatalf("err=%v secondary=%s", err, &secondary)
	}
}

func TestNewTeeLoggerRejectsMissingLocalHandler(t *testing.T) {
	logger, err := NewTeeLogger(nil, slog.NewTextHandler(os.Stdout, nil))
	if !errors.Is(err, errLocalHandlerMissing) {
		t.Fatalf("logger=%v err=%v", logger, err)
	}
}

type writeFunc func([]byte) (int, error)

func (f writeFunc) Write(p []byte) (int, error) { return f(p) }

func readLogText(t *testing.T, path string) string {
	t.Helper()
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}
