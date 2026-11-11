package logging

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/li9chuan/aifirstdev/aifd-log/testdata/other"
)

type statMissingTag struct{ Value string }

type statNestedEvent struct {
	Value []int `stat:"value"`
}

type statDuplicateColumns struct {
	A string `stat:"value"`
	B string `stat:"value"`
}

type statScalar int

type statUIDEvent struct {
	UID int64 `stat:"uid"`
}

type statEmptyEvent struct{}

type statCategory string

type statScalarEvent struct {
	Category statCategory `stat:"category"`
	Enabled  bool         `stat:"enabled"`
	Signed   int8         `stat:"signed"`
	Unsigned uint64       `stat:"unsigned"`
	Ratio    float32      `stat:"ratio"`
	Ignored  []int        `stat:"-"`
}

type statPointerEvent struct {
	Count int `stat:"count"`
}

func TestStatFirstUseCachesLayoutAndNormalizesValuesPointersAndAliases(t *testing.T) {
	type JobAlias = JobFinished
	for _, pointerFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("pointer_first=%t", pointerFirst), func(t *testing.T) {
			path, _ := initStatTest(t, StatConfig{})
			if len(currentStat().events) != 0 {
				t.Fatal("events existed before first use")
			}
			events := []any{JobFinished{"value", 1}, &JobFinished{"pointer", 2}, JobAlias{"alias", 3}}
			wantValues := []string{"value", "pointer", "alias"}
			if pointerFirst {
				events[0], events[1] = events[1], events[0]
				wantValues[0], wantValues[1] = wantValues[1], wantValues[0]
			}
			if err := RecordStat(events[0], statTestBuild); err != nil {
				t.Fatal(err)
			}
			definition := currentStat().events[reflect.TypeFor[JobFinished]()]
			if definition.name != "JobFinished" || definition.layout == nil {
				t.Fatalf("first use definition = %+v", definition)
			}
			for _, event := range events[1:] {
				if err := RecordStat(event, statTestBuild); err != nil {
					t.Fatal(err)
				}
			}
			state := currentStat()
			if len(state.events) != 1 || len(state.names) != 1 || state.events[reflect.TypeFor[JobFinished]()].layout != definition.layout {
				t.Fatal("values, pointers and aliases did not reuse the same layout")
			}
			rows := readStatRows(t, path)
			if len(rows) != 3 {
				t.Fatalf("rows=%v", rows)
			}
			for i, want := range wantValues {
				if rows[i][1] != "JobFinished" || rows[i][5] != want {
					t.Fatalf("event name or payload changed: %v", rows[i])
				}
			}
		})
	}
}

func TestStatCrossPackageNameConflictPreservesOriginalEvent(t *testing.T) {
	path, _ := initStatTest(t, StatConfig{})
	if err := RecordStat(JobFinished{"original", 1}, statTestBuild); err != nil {
		t.Fatal(err)
	}
	err := RecordStat(&other.JobFinished{Result: "conflicting"}, func(string) any {
		t.Fatal("conflicting event invoked header builder")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "already belongs") ||
		!strings.Contains(err.Error(), "("+reflect.TypeFor[JobFinished]().PkgPath()+")") ||
		!strings.Contains(err.Error(), "("+reflect.TypeFor[other.JobFinished]().PkgPath()+")") {
		t.Fatalf("conflict should identify both packages: %v", err)
	}
	if err := RecordStat(JobFinished{"still original", 2}, statTestBuild); err != nil {
		t.Fatal(err)
	}
	if err := RecordStat(LoginSucceeded{"token"}, statTestBuild); err != nil {
		t.Fatal(err)
	}
	rows := readStatRows(t, path)
	if len(rows) != 3 || rows[0][1] != "JobFinished" || rows[1][5] != "still original" || rows[2][1] != "LoginSucceeded" || len(currentStat().events) != 2 || len(currentStat().names) != 2 {
		t.Fatalf("rows=%v cached=%d", rows, len(currentStat().events))
	}
	if got := GetStatCounters(); got.RecordErrors != 1 || got.LocalWritten != 3 {
		t.Fatalf("counters=%+v", got)
	}
}

func TestStatTypeNamesRemainCaseSensitive(t *testing.T) {
	type jobFinished JobFinished
	path, _ := initStatTest(t, StatConfig{})
	for _, event := range []any{JobFinished{}, jobFinished{}} {
		if err := RecordStat(event, statTestBuild); err != nil {
			t.Fatal(err)
		}
	}
	rows := readStatRows(t, path)
	if len(rows) != 2 || rows[0][1] != "JobFinished" || rows[1][1] != "jobFinished" {
		t.Fatalf("names were transformed: %v", rows)
	}
}

func TestInvalidStatEventsFailOnFirstUseBeforeWritingOrForwarding(t *testing.T) {
	pointer := &JobFinished{}
	for name, event := range map[string]any{
		"missing tag": statMissingTag{}, "unsupported": statNestedEvent{}, "duplicate": statDuplicateColumns{},
		"scalar": statScalar(1), "map": map[string]int{}, "slice": []int{}, "function": func() {},
		"nil": nil, "typed nil": (*statPointerEvent)(nil), "pointer to pointer": &pointer,
		"anonymous": struct {
			Value string `stat:"value"`
		}{},
		"anonymous pointer": &struct {
			Value string `stat:"value"`
		}{},
	} {
		t.Run(name, func(t *testing.T) {
			path, closeLog := initStatTest(t, StatConfig{Forward: func(_ context.Context, _ string) error { t.Error("invalid event forwarded"); return nil }})
			if err := RecordStat(event, func(string) any { t.Fatal("invalid event read header"); return nil }); err == nil {
				t.Fatal("invalid event accepted")
			}
			if err := closeLog(); err != nil {
				t.Fatal(err)
			}
			if got := GetStatCounters(); got.RecordErrors != 1 || got.LocalWritten != 0 {
				t.Fatalf("counters=%+v", got)
			}
			if len(readStatRows(t, path)) != 0 || len(currentStat().events) != 0 || len(currentStat().names) != 0 {
				t.Fatal("invalid event polluted file or cache")
			}
		})
	}
}

func TestStatInvalidLayoutDoesNotReserveTypeName(t *testing.T) {
	type JobFinished struct{ MissingTag string }
	path, _ := initStatTest(t, StatConfig{})
	if err := RecordStat(JobFinished{}, statTestBuild); err == nil {
		t.Fatal("missing tag accepted")
	}
	if err := RecordStat(other.JobFinished{Result: "valid"}, statTestBuild); err != nil {
		t.Fatalf("invalid layout reserved the event name: %v", err)
	}
	if rows := readStatRows(t, path); len(rows) != 1 || rows[0][1] != "JobFinished" || rows[0][5] != "valid" {
		t.Fatalf("rows=%v", rows)
	}
}

func TestStatChecksHeaderCombinationOnUseAndSupportsPointers(t *testing.T) {
	path, _ := initStatTest(t, StatConfig{})
	if err := ValidateStatHeader[statTestHeader](); err != nil {
		t.Fatal(err)
	}
	if err := ValidateStatHeader[statMissingTag](); err == nil {
		t.Fatal("invalid header accepted")
	}
	if err := RecordStat(statUIDEvent{7}, statTestBuild); err == nil {
		t.Fatal("header/event collision accepted")
	}
	if err := RecordStat(statEmptyEvent{}, func(string) any { return struct{}{} }); err == nil {
		t.Fatal("zero-column row accepted")
	}
	if err := RecordStat(&statPointerEvent{Count: 3}, statTestBuild); err != nil {
		t.Fatal(err)
	}
	if got := readStatRows(t, path); len(got) != 1 || got[0][1] != "statPointerEvent" || got[0][5] != "3" {
		t.Fatalf("rows=%v", got)
	}
}

func TestConcurrentFirstUsePublishesOneWinnerForConflictingTypes(t *testing.T) {
	path, _ := initStatTest(t, StatConfig{})
	start := make(chan struct{})
	var accepted atomic.Int64
	var workers sync.WaitGroup
	for i := 0; i < 32; i++ {
		workers.Go(func() {
			<-start
			var event any = &JobFinished{JobType: "original"}
			if i%2 == 0 {
				event = other.JobFinished{Result: "duplicate"}
			}
			err := RecordStat(event, statTestBuild)
			if err == nil {
				accepted.Add(1)
			} else if !strings.Contains(err.Error(), "already belongs") {
				t.Error(err)
			}
		})
	}
	close(start)
	workers.Wait()
	rows := readStatRows(t, path)
	if accepted.Load() != 16 || len(rows) != 16 || len(currentStat().events) != 1 || len(currentStat().names) != 1 {
		t.Fatalf("accepted=%d rows=%d", accepted.Load(), len(rows))
	}
	for _, row := range rows {
		if row[1] != "JobFinished" || row[5] != rows[0][5] {
			t.Fatal("conflicting types mixed in output")
		}
	}
}

func TestDisabledStatSkipsTypeValidationAndHeader(t *testing.T) {
	initStatTest(t, StatConfig{})
	closeLog, err := InitDefault(Config{LocalPath: filepath.Join(t.TempDir(), "disabled.log")})
	if err != nil {
		t.Fatal(err)
	}
	defer closeLog()
	if err := RecordStat(struct{}{}, func(string) any { t.Fatal("disabled Stat read header"); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(currentStat().events) != 0 || GetStatCounters() != (StatCounters{}) {
		t.Fatal("disabled Stat cached or counted an event")
	}
}
