package logging

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type statTestHeader struct {
	HeaderVersion int       `stat:"header_version"`
	EventName     string    `stat:"event_name"`
	EventVersion  int       `stat:"event_version"`
	Time          time.Time `stat:"time"`
	UID           int64     `stat:"uid"`
}

type JobFinished struct {
	JobType string `stat:"job_type"`
	Count   int64  `stat:"count"`
}

type LoginSucceeded struct {
	Method string `stat:"method"`
}

func statTestBuild(eventName string) any {
	// Both versions are application-owned columns, independent of type naming.
	return statTestHeader{9, eventName, 2, time.Date(2026, 1, 2, 3, 4, 5, 123, time.UTC), 1001}
}

func initStatTest(t *testing.T, config StatConfig) (string, CloseFunc) {
	t.Helper()
	defaultMu.RLock()
	logger, state := defaultLogger, defaultStat
	defaultMu.RUnlock()
	if config.LocalPath == "" {
		config.LocalPath = filepath.Join(t.TempDir(), "stats", "stat.log")
	}
	closeLog, err := InitDefault(Config{LocalPath: filepath.Join(filepath.Dir(config.LocalPath), "run.log"), Stat: &config})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = closeLog()
		defaultMu.Lock()
		defaultLogger, defaultStat = logger, state
		defaultMu.Unlock()
	})
	return config.LocalPath, closeLog
}

func readStatRows(t *testing.T, path string) [][]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reader := csv.NewReader(strings.NewReader(string(raw)))
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestTypedStatCSVPreservesHeaderOrderVersionsAndEscaping(t *testing.T) {
	path, closeLog := initStatTest(t, StatConfig{})
	if err := ValidateStatHeader[statTestHeader](); err != nil {
		t.Fatal(err)
	}
	text := "import,\"quoted\"\n你好"
	if err := RecordStat(&JobFinished{text, 0}, statTestBuild); err != nil {
		t.Fatal(err)
	}
	if err := RecordStat(LoginSucceeded{"password"}, statTestBuild); err != nil {
		t.Fatal(err)
	}
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"9", "JobFinished", "2", "2026-01-02T03:04:05.000000123Z", "1001", text, "0"},
		{"9", "LoginSucceeded", "2", "2026-01-02T03:04:05.000000123Z", "1001", "password"},
	}
	if got := readStatRows(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %#v, want %#v", got, want)
	}
	if got := GetStatCounters(); got.LocalWritten != 2 || got.RecordErrors != 0 {
		t.Fatalf("counters = %+v", got)
	}
}

func TestStatHeaderIsEntirelyApplicationOwned(t *testing.T) {
	path, _ := initStatTest(t, StatConfig{})
	type customHeader struct {
		Tenant string `stat:"tenant"`
	}
	if err := RecordStat(JobFinished{"import", 1}, func(string) any { return customHeader{"tenant-7"} }); err != nil {
		t.Fatal(err)
	}
	if got := readStatRows(t, path); !reflect.DeepEqual(got, [][]string{{"tenant-7", "import", "1"}}) {
		t.Fatalf("framework added or reordered columns: %#v", got)
	}
}

func TestStatLogSuffixPreservesRawCSVAndReopensInAppendMode(t *testing.T) {
	dir := t.TempDir()
	path, closeLog := initStatTest(t, StatConfig{LocalPath: filepath.Join(dir, "stat.log")})
	if err := RecordStat(JobFinished{"first", 1}, statTestBuild); err != nil {
		t.Fatal(err)
	}
	Info("ordinary log stays separate")
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}
	_, closeReopened := initStatTest(t, StatConfig{LocalPath: path})
	if err := RecordStat(JobFinished{"second", 2}, statTestBuild); err != nil {
		t.Fatal(err)
	}
	if err := closeReopened(); err != nil {
		t.Fatal(err)
	}
	rows := readStatRows(t, path)
	if len(rows) != 2 || rows[0][5] != "first" || rows[1][5] != "second" {
		t.Fatalf("Stat file was wrapped as JSON, mixed with ordinary logs or truncated: %#v", rows)
	}
}

func TestTypedStatFreezesValuesBeforeHTTPForwarding(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received = string(raw)
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	path, closeLog := initStatTest(t, StatConfig{Forward: func(ctx context.Context, record string) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader(record))
		if err != nil {
			return err
		}
		response, err := server.Client().Do(req)
		if err != nil {
			return err
		}
		return response.Body.Close()
	}})
	header := statTestBuild("JobFinished").(statTestHeader)
	payload := &JobFinished{"original", 7}
	if err := RecordStat(payload, func(string) any { return &header }); err != nil {
		t.Fatal(err)
	}
	payload.JobType, header.UID = "changed", 999
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("forward did not reach receiver")
	}
	unblock()
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != received || !strings.Contains(received, "1001,original,7\n") {
		t.Fatalf("local/remote differ: local=%q remote=%q", raw, received)
	}
	if got := GetStatCounters(); got.Forwarded != 1 || got.RecordErrors != 0 {
		t.Fatalf("counters = %+v", got)
	}
}

func TestUnavailableForwarderDoesNotChangeLocalRecords(t *testing.T) {
	path, closeLog := initStatTest(t, StatConfig{Forward: func(context.Context, string) error { return errors.New("receiver unavailable") }})
	for i := range 4 {
		if err := RecordStat(JobFinished{"import", int64(i)}, statTestBuild); err != nil {
			t.Fatal(err)
		}
	}
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}
	if got := readStatRows(t, path); len(got) != 4 {
		t.Fatalf("lost local backup: %#v", got)
	}
	if got := GetStatCounters(); got.LocalWritten != 4 || got.ForwardErrors != 4 {
		t.Fatalf("counters = %+v", got)
	}
}

func TestFullForwardQueueDropsOnlyRemoteCopies(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	path, closeLog := initStatTest(t, StatConfig{ForwardQueueSize: 1, Forward: func(ctx context.Context, record string) error {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}})
	if err := RecordStat(JobFinished{"first", 1}, statTestBuild); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("forward worker did not start")
	}
	for _, text := range []string{"queued", "dropped"} {
		if err := RecordStat(JobFinished{text, 1}, statTestBuild); err != nil {
			t.Fatal(err)
		}
	}
	if got := readStatRows(t, path); len(got) != 3 {
		t.Fatalf("remote pressure lost local records: %#v", got)
	}
	close(release)
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}
	if got := GetStatCounters(); got.LocalWritten != 3 || got.Forwarded != 2 || got.ForwardDropped != 1 {
		t.Fatalf("counters = %+v", got)
	}
}

func TestStatCloseIsBoundedAndCancelsRemoteCall(t *testing.T) {
	entered, cancelled := make(chan struct{}), make(chan struct{})
	path, closeLog := initStatTest(t, StatConfig{ForwardTimeout: time.Hour, ForwardDrainTimeout: 20 * time.Millisecond, Forward: func(ctx context.Context, record string) error {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	}})
	if err := RecordStat(JobFinished{"in flight", 1}, statTestBuild); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("forward did not start")
	}
	if err := RecordStat(JobFinished{"pending", 2}, statTestBuild); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- closeLog() }()
	select {
	case err := <-closed:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("close = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("close ignored drain deadline")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("forward context was not cancelled")
	}
	if err := RecordStat(JobFinished{}, statTestBuild); !errors.Is(err, ErrStatClosed) {
		t.Fatalf("append after close = %v", err)
	}
	if len(readStatRows(t, path)) != 2 || GetStatCounters().ForwardDropped != 1 {
		t.Fatal("drain timeout damaged local backup or pending accounting")
	}
	if err := closeLog(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("repeated close = %v", err)
	}
}

func TestConcurrentLocalAppendAndCloseHaveNoPartialRecords(t *testing.T) {
	path, closeLog := initStatTest(t, StatConfig{})
	var successful atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	firstWritten := make(chan struct{}, 8)
	for worker := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for index := range 80 {
				err := RecordStat(JobFinished{fmt.Sprint(worker), int64(index)}, statTestBuild)
				if err == nil {
					successful.Add(1)
				} else if !errors.Is(err, ErrStatClosed) {
					t.Errorf("record: %v", err)
				}
				if index == 0 {
					firstWritten <- struct{}{}
				}
			}
		}()
	}
	close(start)
	for range 8 {
		<-firstWritten
	}
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if got := len(readStatRows(t, path)); int64(got) != successful.Load() {
		t.Fatalf("rows=%d accepted=%d", got, successful.Load())
	}
}

func TestStatFailuresAreCountedWithoutWritingMalformedRows(t *testing.T) {
	path, _ := initStatTest(t, StatConfig{})
	for _, payload := range []any{nil, (*JobFinished)(nil), statScalar(42)} {
		if err := RecordStat(payload, statTestBuild); err == nil {
			t.Fatalf("invalid payload accepted: %#v", payload)
		}
	}
	if err := RecordStat(JobFinished{}, nil); err == nil {
		t.Fatal("missing builder accepted")
	}
	if err := RecordStat(JobFinished{}, func(string) any { panic("builder failed") }); err == nil {
		t.Fatal("builder panic was hidden")
	}
	if err := RecordStat(JobFinished{}, func(string) any { return (*statTestHeader)(nil) }); err == nil {
		t.Fatal("nil header accepted")
	}
	if err := RecordStat(JobFinished{}, statTestBuild); err != nil {
		t.Fatal(err)
	}
	if len(readStatRows(t, path)) != 1 || GetStatCounters().RecordErrors != 6 {
		t.Fatalf("counters=%+v", GetStatCounters())
	}
}

func TestStatDisabledSkipsBuilderAndFailedInitPreservesPreviousLogger(t *testing.T) {
	path, _ := initStatTest(t, StatConfig{})
	previous := currentStat()
	bad := filepath.Join(t.TempDir(), "same.log")
	for _, config := range []Config{
		{LocalPath: bad, Stat: &StatConfig{LocalPath: bad}},
		{LocalPath: bad, Stat: &StatConfig{LocalPath: t.TempDir()}},
		{LocalPath: bad, Stat: &StatConfig{}},
		{LocalPath: bad, Stat: &StatConfig{LocalPath: path, ForwardQueueSize: -1}},
	} {
		if closeLog, err := InitDefault(config); err == nil {
			_ = closeLog()
			t.Fatal("invalid init succeeded")
		}
		if currentStat() != previous {
			t.Fatal("failed init replaced the active Stat logger")
		}
	}
	if err := RecordStat(JobFinished{}, statTestBuild); err != nil {
		t.Fatal(err)
	}
	closeDisabled, err := InitDefault(Config{LocalPath: filepath.Join(t.TempDir(), "disabled.log")})
	if err != nil {
		t.Fatal(err)
	}
	defer closeDisabled()
	if err := RecordStat(nil, func(string) any { t.Fatal("disabled Stat called builder"); return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestTypedStatDoesNotDuplicateIntoOrdinaryLog(t *testing.T) {
	path, _ := initStatTest(t, StatConfig{})
	if err := RecordStat(JobFinished{}, statTestBuild); err != nil {
		t.Fatal(err)
	}
	if len(readStatRows(t, path)) != 1 {
		t.Fatal("typed Stat was duplicated")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), "run.log"))
	if err != nil || len(raw) != 0 {
		t.Fatalf("typed Stat appeared in ordinary log: %q, %v", raw, err)
	}
}

func TestTypedStatSizeRotationKeepsCompleteCSVRecords(t *testing.T) {
	path, closeLog := initStatTest(t, StatConfig{Rotation: RotationConfig{MaxSizeMB: 1}})
	method := strings.Repeat("a", 600*1024) + ",\"quoted\"\nsecond line"
	for i := 0; i < 2; i++ {
		if err := RecordStat(LoginSucceeded{Method: method}, statTestBuild); err != nil {
			t.Fatal(err)
		}
	}
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}
	archives := logArchives(t, path)
	if len(archives) != 1 {
		t.Fatalf("archives=%v", archives)
	}
	for _, file := range append(archives, path) {
		rows := readStatRows(t, file)
		if len(rows) != 1 || rows[0][len(rows[0])-1] != method {
			t.Fatalf("CSV split or changed in %s", file)
		}
	}
	if got := GetStatCounters().LocalWritten; got != 2 {
		t.Fatalf("written=%d", got)
	}
}

func TestStatFileConfigConvertsOutputOptions(t *testing.T) {
	config, err := (FileConfig{Stat: &StatFileConfig{LocalPath: "stat.log", ForwardTimeout: 3 * time.Second}}).ToConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Stat == nil || config.Stat.LocalPath != "stat.log" || config.Stat.ForwardTimeout != 3*time.Second || config.Stat.Forward != nil {
		t.Fatalf("config = %+v", config.Stat)
	}
	if _, err := (FileConfig{Stat: &StatFileConfig{}}).ToConfig(); err == nil {
		t.Fatal("enabled Stat without local path accepted")
	}
}

func TestStatSingleEmptyFieldIsARealCSVRecord(t *testing.T) {
	path, _ := initStatTest(t, StatConfig{})
	if err := RecordStat(LoginSucceeded{}, func(string) any { return struct{}{} }); err != nil {
		t.Fatal(err)
	}
	if got := readStatRows(t, path); !reflect.DeepEqual(got, [][]string{{""}}) {
		t.Fatalf("single empty field disappeared: %#v", got)
	}
}

func TestStatSupportsScalarAliasesAndPreservesApplicationTimezone(t *testing.T) {
	type header struct {
		Time time.Time `stat:"occurred_at"`
	}
	path, _ := initStatTest(t, StatConfig{})
	if err := RecordStat(statScalarEvent{"import", true, -2, ^uint64(0), 1.25, nil}, func(string) any {
		return header{time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("app", 8*60*60))}
	}); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"2026-01-02T03:04:05+08:00", "import", "true", "-2", "18446744073709551615", "1.25"}}
	if got := readStatRows(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows=%#v", got)
	}
}

func TestStatForwardTimeoutAndPanicDoNotStopFollowingRecords(t *testing.T) {
	var calls atomic.Int64
	path, closeLog := initStatTest(t, StatConfig{ForwardTimeout: 20 * time.Millisecond, Forward: func(ctx context.Context, _ string) error {
		switch calls.Add(1) {
		case 1:
			<-ctx.Done()
			return ctx.Err()
		case 2:
			panic("transport bug")
		}
		return nil
	}})
	for i := range 3 {
		if err := RecordStat(JobFinished{"import", int64(i)}, statTestBuild); err != nil {
			t.Fatal(err)
		}
	}
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}
	if got := GetStatCounters(); got.LocalWritten != 3 || got.ForwardErrors != 2 || got.Forwarded != 1 || len(readStatRows(t, path)) != 3 {
		t.Fatalf("counters=%+v", got)
	}
}

func TestStatLocalWriteFailureDoesNotPretendToAcceptARecord(t *testing.T) {
	path, _ := initStatTest(t, StatConfig{Forward: func(context.Context, string) error { t.Error("unrecorded event was forwarded"); return nil }})
	if err := currentStat().recorder.file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RecordStat(JobFinished{}, statTestBuild); err == nil {
		t.Fatal("local write failure hidden")
	}
	if got := GetStatCounters(); got.LocalWritten != 0 || got.RecordErrors != 1 || len(readStatRows(t, path)) != 0 {
		t.Fatalf("counters=%+v", got)
	}
}

func TestUnrelatedServiceEventCanUseItsOwnHeader(t *testing.T) {
	path, _ := initStatTest(t, StatConfig{})
	if err := ValidateStatHeader[statTestHeader](); err != nil {
		t.Fatal(err)
	}
	type serviceHeader struct {
		Origin string `stat:"origin"`
	}
	if err := RecordStat(statUIDEvent{7}, func(string) any { return serviceHeader{"worker"} }); err != nil {
		t.Fatal(err)
	}
	if got := readStatRows(t, path); !reflect.DeepEqual(got, [][]string{{"worker", "7"}}) {
		t.Fatalf("rows=%v", got)
	}
}
