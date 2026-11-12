package logging

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func recoveryHeader(eventName string) any {
	return struct {
		Event string `stat:"event"`
	}{eventName}
}

func TestStatRestartPreservesValidCSVAndQuarantinesIncompleteTail(t *testing.T) {
	prefix := "OldEvent,first\nJobFinished,\"你好\nold row\",2\n\"\"\n"
	for name, tail := range map[string]string{
		"unclosed quote":       "JobFinished,\"unfinished\nstill open",
		"missing newline":      "JobFinished,unfinished,3",
		"closed quote no LF":   "JobFinished,\"complete looking\",3",
		"invalid unquoted":     "JobFinished,un\"quoted,3\n",
		"valid terminated CSV": "OtherEvent,done\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "stat.log")
			if err := os.WriteFile(path, []byte(prefix+tail), 0600); err != nil {
				t.Fatal(err)
			}
			var forwarded []string
			_, closeLog := initStatTest(t, StatConfig{LocalPath: path, Rotation: RotationConfig{DisableDaily: true}, Forward: func(_ context.Context, record string) error {
				forwarded = append(forwarded, record)
				return nil
			}})
			if err := RecordStat(JobFinished{"new", 1}, recoveryHeader); err != nil {
				t.Fatal(err)
			}
			if err := closeLog(); err != nil {
				t.Fatal(err)
			}
			want := prefix
			quarantines, err := filepath.Glob(path + ".corrupt-*")
			if err != nil {
				t.Fatal(err)
			}
			if name == "valid terminated CSV" {
				want += tail
				if len(quarantines) != 0 {
					t.Fatalf("valid CSV was quarantined: %v", quarantines)
				}
			} else {
				if len(quarantines) != 1 || readLogText(t, quarantines[0]) != tail {
					t.Fatalf("damaged tail was not preserved exactly: %v", quarantines)
				}
			}
			want += "JobFinished,new,1\n"
			if raw := readLogText(t, path); raw != want {
				t.Fatalf("recovered CSV changed valid records: %q want=%q", raw, want)
			}
			reader := csv.NewReader(strings.NewReader(want))
			reader.FieldsPerRecord = -1
			if _, err := reader.ReadAll(); err != nil {
				t.Fatal(err)
			}
			if len(forwarded) != 1 || forwarded[0] != "JobFinished,new,1\n" {
				t.Fatalf("recovery replayed or changed forwarding: %v", forwarded)
			}
			if got := GetStatCounters(); got.LocalWritten != 1 || got.RecordErrors != 0 || got.Forwarded != 1 {
				t.Fatalf("counters=%+v", got)
			}
		})
	}
}

func TestStatRecoveryKeepsPreviousDayRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stat.log")
	prefix, tail := "OldEvent,good\n", "JobFinished,\"unfinished\n"
	if err := os.WriteFile(path, []byte(prefix+tail), 0600); err != nil {
		t.Fatal(err)
	}
	yesterday := time.Now().UTC().Add(-24 * time.Hour)
	if err := os.Chtimes(path, yesterday, yesterday); err != nil {
		t.Fatal(err)
	}
	_, closeLog := initStatTest(t, StatConfig{LocalPath: path, Rotation: RotationConfig{UTC: true}})
	if err := RecordStat(JobFinished{"today", 1}, recoveryHeader); err != nil {
		t.Fatal(err)
	}
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}
	archives := logArchives(t, path)
	if len(archives) != 1 || readLogText(t, archives[0]) != prefix || readLogText(t, path) != "JobFinished,today,1\n" {
		t.Fatalf("recovery mixed dates or archived a broken tail: %v", archives)
	}
}

func TestStatRecoveryBoundaryDoesNotTreatReadFailureAsSyntaxDamage(t *testing.T) {
	sentinel := errors.New("injected read failure")
	for _, prefix := range []string{"OldEvent,good\n", "OldEvent,good\nBroken,\"unfinished"} {
		input := io.MultiReader(strings.NewReader(prefix), recoveryReadError{sentinel})
		if _, err := statCSVBoundary(input, int64(len(prefix)+10), true); !errors.Is(err, sentinel) {
			t.Fatalf("read failure was treated as a recoverable tail: %v", err)
		}
	}
	if _, err := statCSVBoundary(strings.NewReader("OldEvent,good\n"), 100, true); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("early EOF was treated as a complete file: %v", err)
	}
}

type recoveryReadError struct{ err error }

func (r recoveryReadError) Read([]byte) (int, error) { return 0, r.err }

func TestOrdinaryLogsDoNotApplyCSVRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stat.log")
	prefix := "ordinary log with \"unfinished text"
	if err := os.WriteFile(path, []byte(prefix), 0600); err != nil {
		t.Fatal(err)
	}
	logger, closeLog, err := NewLocalFirstLogger(Config{LocalPath: path, Rotation: RotationConfig{DisableDaily: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeLog() })
	logger.Info(context.Background(), "new message")
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}
	if raw := readLogText(t, path); !strings.HasPrefix(raw, prefix) || !strings.Contains(raw, "new message") {
		t.Fatalf("ordinary log was rewritten: %q", raw)
	}
	if files, _ := filepath.Glob(path + ".corrupt-*"); len(files) != 0 {
		t.Fatalf("ordinary log was interpreted as CSV: %v", files)
	}
}

func TestArchiveFailureReachesStderrAndLoggerClose(t *testing.T) {
	if os.Getenv("AIFD_LOG_DIAGNOSTIC_CHILD") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestArchiveFailureReachesStderrAndLoggerClose$")
		cmd.Env = append(os.Environ(), "AIFD_LOG_DIAGNOSTIC_CHILD=1")
		output, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(output), "aifd-log: log archive maintenance") || !strings.Contains(string(output), ".log.gz") {
			t.Fatalf("background error absent from child stderr: %v\n%s", err, output)
		}
		return
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "service.log")
	archive := filepath.Join(dir, "service-"+time.Now().UTC().Add(-time.Hour).Format("2006-01-02T15-04-05.000")+".log")
	if err := os.WriteFile(archive, []byte("archive\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(archive+".gz", 0700); err != nil {
		t.Fatal(err)
	}
	_, closeLog, err := NewLocalFirstLogger(Config{LocalPath: path, Rotation: RotationConfig{Compress: true, UTC: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeLog() })
	if err := closeLog(); err == nil || !strings.Contains(err.Error(), "archive maintenance") {
		t.Fatalf("archive error lost by logger Close: %v", err)
	}
}
