package user

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/li9chuan/aifirstdev/aifd-log"
	"github.com/li9chuan/aifirstdev/aifd-log/testdata/other"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type JobFinished struct {
	Result string `stat:"result"`
}

type LoginSucceeded struct {
	Method string `stat:"method"`
}

type runtimeStatCollision struct {
	UID int64 `stat:"uid"`
}

type runtimeStatHeader struct {
	Version      int    `stat:"header_version"`
	Event        string `stat:"event"`
	EventVersion int    `stat:"event_version"`
	UID          int64  `stat:"uid"`
	State        int64  `stat:"state"`
	Capture      int    `stat:"capture"`
}

type runtimeStatModule struct {
	*testModule
	routes     int
	emitOnLoad bool
}

func (m *runtimeStatModule) RegisterRPC(*rpc.Registry) error { m.routes++; return nil }
func (m *runtimeStatModule) OnLoad(ctx context.Context, owner *User[*wrapperspb.Int64Value]) error {
	if err := m.testModule.OnLoad(ctx, owner); err != nil {
		return err
	}
	if m.emitOnLoad {
		owner.Stat(JobFinished{Result: "loaded"})
	}
	return nil
}

func setupRuntimeStat(t *testing.T, enabled bool) string {
	t.Helper()
	dir := t.TempDir()
	config := logging.Config{LocalPath: filepath.Join(dir, "run.log")}
	path := filepath.Join(dir, "stat.log")
	if enabled {
		config.Stat = &logging.StatConfig{LocalPath: path}
	}
	previous := logging.Default()
	closeLog, err := logging.InitDefault(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := closeLog(); err != nil {
			t.Error(err)
		}
		logging.SetDefault(previous)
	})
	return path
}

func TestUserStatDiscoversEventsOnUseAndBuildsEachHeader(t *testing.T) {
	ctx := context.Background()
	path := setupRuntimeStat(t, true)
	setupMemoryDB(t)
	first := &runtimeStatModule{testModule: &testModule{}, emitOnLoad: true}
	second := &runtimeStatModule{testModule: &testModule{}}
	modules := NewManager[*wrapperspb.Int64Value](first)
	modules.Register(second)
	captures := 0
	rt := NewRuntime(rpc.NewRegistry(), newInt64Store(t), modules, WithStatHeader(func(owner *User[*wrapperspb.Int64Value], eventName string) runtimeStatHeader {
		captures++
		var state int64
		if err := owner.Read(func(value *wrapperspb.Int64Value) { state = value.Value }); err != nil {
			t.Fatal(err)
		}
		return runtimeStatHeader{9, eventName, 2, owner.UID(), state, captures}
	}))
	if err := rt.RegisterRPC(); err != nil {
		t.Fatal(err)
	}
	u, err := rt.CreateUser(ctx, 1001)
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Mutate(ctx, func(value *wrapperspb.Int64Value) error { value.Value = 77; return nil }); err != nil {
		t.Fatal(err)
	}
	u.Stat(JobFinished{Result: "success"})
	other, err := rt.CreateUser(ctx, 1002)
	if err != nil {
		t.Fatal(err)
	}
	other.Stat(&JobFinished{Result: "other"})
	other.Stat(LoginSucceeded{Method: "token"})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"9", "JobFinished", "2", "1001", "1001", "1", "loaded"},
		{"9", "JobFinished", "2", "1001", "77", "2", "success"},
		{"9", "JobFinished", "2", "1002", "1002", "3", "loaded"},
		{"9", "JobFinished", "2", "1002", "1002", "4", "other"},
		{"9", "LoginSucceeded", "2", "1002", "1002", "5", "token"},
	}
	if !reflect.DeepEqual(rows, want) || first.routes != 1 || second.routes != 1 {
		t.Fatalf("rows=%#v routes=%d/%d", rows, first.routes, second.routes)
	}
	if counters := logging.GetStatCounters(); counters.LocalWritten != 5 || counters.RecordErrors != 0 {
		t.Fatalf("counters=%+v", counters)
	}
}

func TestUserStatCrossPackageNameConflictIsReportedWithoutChangingUser(t *testing.T) {
	setupRuntimeStat(t, true)
	setupMemoryDB(t)
	module := &runtimeStatModule{testModule: &testModule{}}
	rt := NewRuntime(rpc.NewRegistry(), newInt64Store(t), NewManager[*wrapperspb.Int64Value](module), WithStatHeader(
		func(owner *User[*wrapperspb.Int64Value], eventName string) runtimeStatHeader {
			return runtimeStatHeader{Event: eventName, UID: owner.UID()}
		},
	))
	if err := rt.RegisterRPC(); err != nil {
		t.Fatal(err)
	}
	u, err := rt.CreateUser(context.Background(), 1001)
	if err != nil {
		t.Fatal(err)
	}
	u.Stat(JobFinished{Result: "success"})
	u.Stat(other.JobFinished{Result: "conflicting"})
	if got := logging.GetStatCounters(); got.RecordErrors != 1 || got.LocalWritten != 1 || u.Snapshot().Value != 1001 {
		t.Fatalf("counters=%+v user=%v", got, u.Snapshot())
	}
	if module.routes != 1 {
		t.Fatal("event discovery changed business route registration")
	}
}

func TestUserStatInvalidPayloadSkipsHeaderAndLeavesUserUnchanged(t *testing.T) {
	path := setupRuntimeStat(t, true)
	setupMemoryDB(t)
	captures := 0
	rt := NewRuntime(rpc.NewRegistry(), newInt64Store(t), nil, WithStatHeader(
		func(owner *User[*wrapperspb.Int64Value], eventName string) runtimeStatHeader {
			captures++
			return runtimeStatHeader{Event: eventName, UID: owner.UID()}
		},
	))
	u, err := rt.CreateUser(context.Background(), 1001)
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []any{(*JobFinished)(nil), struct{}{}, 42} {
		u.Stat(payload)
	}
	if got := logging.GetStatCounters(); got.RecordErrors != 3 || got.LocalWritten != 0 || captures != 0 || u.Snapshot().Value != 1001 {
		t.Fatalf("counters=%+v captures=%d user=%v", got, captures, u.Snapshot())
	}
	u.Stat(&JobFinished{Result: "valid pointer"})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	if err != nil || len(rows) != 1 || rows[0][1] != "JobFinished" || rows[0][6] != "valid pointer" || captures != 1 {
		t.Fatalf("rows=%v captures=%d err=%v", rows, captures, err)
	}
}

func TestUserStatHeaderValidationRejectsWrongUserNilBuilderAndUnsupportedFields(t *testing.T) {
	type invalidHeader struct {
		Data map[string]int `stat:"data"`
	}
	var nilBuilder func(*User[*wrapperspb.Int64Value], string) runtimeStatHeader
	for name, option := range map[string]Option{
		"wrong user type": WithStatHeader(func(*User[*wrapperspb.StringValue], string) runtimeStatHeader { return runtimeStatHeader{} }),
		"nil builder":     WithStatHeader(nilBuilder),
		"invalid header":  WithStatHeader(func(*User[*wrapperspb.Int64Value], string) invalidHeader { return invalidHeader{} }),
	} {
		t.Run(name, func(t *testing.T) {
			setupRuntimeStat(t, true)
			rt := NewRuntime(rpc.NewRegistry(), (*Store[*wrapperspb.Int64Value])(nil), nil, option)
			if err := rt.RegisterRPC(); err == nil {
				t.Fatal("invalid header configuration accepted")
			}
		})
	}
}

func TestUserStatDisabledDoesNotReadApplicationHeader(t *testing.T) {
	setupRuntimeStat(t, false)
	setupMemoryDB(t)
	module := &runtimeStatModule{testModule: &testModule{}}
	rt := NewRuntime(rpc.NewRegistry(), newInt64Store(t), NewManager[*wrapperspb.Int64Value](module), WithStatHeader(func(*User[*wrapperspb.Int64Value], string) runtimeStatHeader {
		t.Fatal("disabled stat called header builder")
		return runtimeStatHeader{}
	}))
	if err := rt.RegisterRPC(); err != nil {
		t.Fatal(err)
	}
	u, err := rt.CreateUser(context.Background(), 1001)
	if err != nil {
		t.Fatal(err)
	}
	u.Stat(JobFinished{})
	if counters := logging.GetStatCounters(); counters != (logging.StatCounters{}) {
		t.Fatalf("disabled counters=%+v", counters)
	}
}

func TestUserStatMissingBuilderReportsFailureWithoutChangingUser(t *testing.T) {
	setupRuntimeStat(t, true)
	setupMemoryDB(t)
	module := &runtimeStatModule{testModule: &testModule{}}
	rt := NewRuntime(rpc.NewRegistry(), newInt64Store(t), NewManager[*wrapperspb.Int64Value](module))
	if err := rt.RegisterRPC(); err != nil {
		t.Fatal(err)
	}
	u, err := rt.CreateUser(context.Background(), 1001)
	if err != nil {
		t.Fatal(err)
	}
	u.Stat(JobFinished{})
	if counters := logging.GetStatCounters(); counters.RecordErrors != 1 || counters.LocalWritten != 0 || u.Snapshot().Value != 1001 {
		t.Fatalf("counters=%+v", counters)
	}
}

func TestUserStatHeaderEventCollisionIsReportedOnFirstUse(t *testing.T) {
	setupRuntimeStat(t, true)
	setupMemoryDB(t)
	module := &runtimeStatModule{testModule: &testModule{}}
	rt := NewRuntime(rpc.NewRegistry(), newInt64Store(t), NewManager[*wrapperspb.Int64Value](module), WithStatHeader(
		func(*User[*wrapperspb.Int64Value], string) runtimeStatHeader { return runtimeStatHeader{} },
	))
	if err := rt.RegisterRPC(); err != nil {
		t.Fatal(err)
	}
	u, err := rt.CreateUser(context.Background(), 1001)
	if err != nil {
		t.Fatal(err)
	}
	u.Stat(runtimeStatCollision{UID: 7})
	if got := logging.GetStatCounters(); got.RecordErrors != 1 || got.LocalWritten != 0 || module.routes != 1 {
		t.Fatalf("counters=%+v routes=%d", got, module.routes)
	}
	u.Stat(JobFinished{Result: "valid"})
	if got := logging.GetStatCounters().LocalWritten; got != 1 {
		t.Fatalf("valid event after failure: %d", got)
	}
}

func TestUserStatNeedsNoFeatureOrRPCRegistration(t *testing.T) {
	setupRuntimeStat(t, true)
	setupMemoryDB(t)
	rt := NewRuntime(rpc.NewRegistry(), newInt64Store(t), nil, WithStatHeader(
		func(owner *User[*wrapperspb.Int64Value], eventName string) runtimeStatHeader {
			return runtimeStatHeader{Event: eventName, UID: owner.UID()}
		},
	))
	u, err := rt.CreateUser(context.Background(), 1001)
	if err != nil {
		t.Fatal(err)
	}
	u.Stat(JobFinished{Result: "standalone"})
	if got := logging.GetStatCounters(); got.LocalWritten != 1 || got.RecordErrors != 0 {
		t.Fatalf("counters=%+v", got)
	}
}
