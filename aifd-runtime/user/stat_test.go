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
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type runtimeStatJob struct {
	Result string `stat:"result"`
}

type runtimeStatLogin struct {
	Method string `stat:"method"`
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
	events     []logging.StatSchema
	routes     int
	emitOnLoad bool
}

func (m *runtimeStatModule) StatEvents() []logging.StatSchema { return m.events }
func (m *runtimeStatModule) RegisterRPC(*rpc.Registry) error  { m.routes++; return nil }
func (m *runtimeStatModule) OnLoad(ctx context.Context, owner *User[*wrapperspb.Int64Value]) error {
	if err := m.testModule.OnLoad(ctx, owner); err != nil {
		return err
	}
	if m.emitOnLoad {
		owner.Stat(runtimeStatJob{Result: "loaded"})
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

func TestUserStatCollectsInitialAndAppendedFeaturesAndBuildsEachHeader(t *testing.T) {
	ctx := context.Background()
	path := setupRuntimeStat(t, true)
	setupMemoryDB(t)
	first := &runtimeStatModule{testModule: &testModule{}, events: []logging.StatSchema{logging.DefineStat[runtimeStatJob]("job_finished", 2)}, emitOnLoad: true}
	second := &runtimeStatModule{testModule: &testModule{}, events: []logging.StatSchema{
		first.events[0], // Repeated definitions do not duplicate records.
		logging.DefineStat[runtimeStatLogin]("login_succeeded", 1),
	}}
	modules := NewManager[*wrapperspb.Int64Value](first)
	modules.Register(second)
	captures := 0
	rt := NewRuntime(rpc.NewRegistry(), newInt64Store(t), modules, WithStatHeader(func(owner *User[*wrapperspb.Int64Value], event logging.StatSchema) runtimeStatHeader {
		captures++
		var state int64
		if err := owner.Read(func(value *wrapperspb.Int64Value) { state = value.Value }); err != nil {
			t.Fatal(err)
		}
		return runtimeStatHeader{9, event.Name, event.Version, owner.UID(), state, captures}
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
	u.Stat(runtimeStatJob{Result: "success"})
	other, err := rt.CreateUser(ctx, 1002)
	if err != nil {
		t.Fatal(err)
	}
	other.Stat(&runtimeStatJob{Result: "other"})
	other.Stat(runtimeStatLogin{Method: "token"})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"9", "job_finished", "2", "1001", "1001", "1", "loaded"},
		{"9", "job_finished", "2", "1001", "77", "2", "success"},
		{"9", "job_finished", "2", "1002", "1002", "3", "loaded"},
		{"9", "job_finished", "2", "1002", "1002", "4", "other"},
		{"9", "login_succeeded", "1", "1002", "1002", "5", "token"},
	}
	if !reflect.DeepEqual(rows, want) || first.routes != 1 || second.routes != 1 {
		t.Fatalf("rows=%#v routes=%d/%d", rows, first.routes, second.routes)
	}
	if counters := logging.GetStatCounters(); counters.LocalWritten != 5 || counters.RecordErrors != 0 {
		t.Fatalf("counters=%+v", counters)
	}
}

func TestUserStatRegistrationConflictPrecedesAllBusinessRoutes(t *testing.T) {
	setupRuntimeStat(t, true)
	first := &runtimeStatModule{testModule: &testModule{}, events: []logging.StatSchema{logging.DefineStat[runtimeStatJob]("job", 1)}}
	second := &runtimeStatModule{testModule: &testModule{}, events: []logging.StatSchema{logging.DefineStat[runtimeStatJob]("different", 1)}}
	rt := NewRuntime(rpc.NewRegistry(), (*Store[*wrapperspb.Int64Value])(nil), NewManager[*wrapperspb.Int64Value](first, second))
	if err := rt.RegisterRPC(); err == nil || !strings.Contains(err.Error(), "already binds") {
		t.Fatalf("conflict=%v", err)
	}
	if first.routes != 0 || second.routes != 0 {
		t.Fatal("business routes were registered before stat validation")
	}
}

func TestUserStatHeaderValidationRejectsWrongUserNilBuilderAndUnsupportedFields(t *testing.T) {
	type invalidHeader struct {
		Data map[string]int `stat:"data"`
	}
	var nilBuilder func(*User[*wrapperspb.Int64Value], logging.StatSchema) runtimeStatHeader
	for name, option := range map[string]Option{
		"wrong user type": WithStatHeader(func(*User[*wrapperspb.StringValue], logging.StatSchema) runtimeStatHeader { return runtimeStatHeader{} }),
		"nil builder":     WithStatHeader(nilBuilder),
		"invalid header":  WithStatHeader(func(*User[*wrapperspb.Int64Value], logging.StatSchema) invalidHeader { return invalidHeader{} }),
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
	module := &runtimeStatModule{testModule: &testModule{}, events: []logging.StatSchema{logging.DefineStat[runtimeStatJob]("job", 1)}}
	rt := NewRuntime(rpc.NewRegistry(), newInt64Store(t), NewManager[*wrapperspb.Int64Value](module), WithStatHeader(func(*User[*wrapperspb.Int64Value], logging.StatSchema) runtimeStatHeader {
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
	u.Stat(runtimeStatJob{})
	if counters := logging.GetStatCounters(); counters != (logging.StatCounters{}) {
		t.Fatalf("disabled counters=%+v", counters)
	}
}

func TestUserStatMissingBuilderReportsFailureWithoutChangingUser(t *testing.T) {
	setupRuntimeStat(t, true)
	setupMemoryDB(t)
	module := &runtimeStatModule{testModule: &testModule{}, events: []logging.StatSchema{logging.DefineStat[runtimeStatJob]("job", 1)}}
	rt := NewRuntime(rpc.NewRegistry(), newInt64Store(t), NewManager[*wrapperspb.Int64Value](module))
	if err := rt.RegisterRPC(); err != nil {
		t.Fatal(err)
	}
	u, err := rt.CreateUser(context.Background(), 1001)
	if err != nil {
		t.Fatal(err)
	}
	u.Stat(runtimeStatJob{})
	if counters := logging.GetStatCounters(); counters.RecordErrors != 1 || counters.LocalWritten != 0 || u.Snapshot().Value != 1001 {
		t.Fatalf("counters=%+v", counters)
	}
}

func TestUserRejectsHeaderAndOwnedEventColumnCollisionBeforeRoutes(t *testing.T) {
	setupRuntimeStat(t, true)
	type collision struct {
		UID int64 `stat:"uid"`
	}
	module := &runtimeStatModule{testModule: &testModule{}, events: []logging.StatSchema{logging.DefineStat[collision]("collision", 1)}}
	rt := NewRuntime(rpc.NewRegistry(), (*Store[*wrapperspb.Int64Value])(nil), NewManager[*wrapperspb.Int64Value](module), WithStatHeader(
		func(*User[*wrapperspb.Int64Value], logging.StatSchema) runtimeStatHeader { return runtimeStatHeader{} },
	))
	if err := rt.RegisterRPC(); err == nil || module.routes != 0 {
		t.Fatalf("error=%v routes=%d", err, module.routes)
	}
}
