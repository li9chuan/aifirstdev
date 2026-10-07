package root

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	base "github.com/li9chuan/aifirstdev/aifd-contracts"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestActivateRunsHooksAndMarksRootActive(t *testing.T) {
	ctx := context.Background()
	setupServiceMemoryDB(t)
	module := &testModule{}
	service := NewRuntime(rpc.NewRegistry(), newServiceStore(t), NewManager[*wrapperspb.Int64Value](module))

	owner, isNew, err := service.Activate(ctx, 1001)
	if err != nil {
		t.Fatalf("Activate error = %v", err)
	}
	if !isNew {
		t.Fatal("isNew = false, want true")
	}
	if owner.ID() != 1001 {
		t.Fatalf("id = %d, want 1001", owner.ID())
	}
	if _, ok := service.GetRoot(1001); !ok {
		t.Fatal("root 1001 is not active")
	}
	want := []string{"load", "activate"}
	if got := module.callsSnapshot(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

func TestConcurrentActivateSameRootRunsFirstLoadOnce(t *testing.T) {
	ctx := context.Background()
	setupServiceMemoryDB(t)
	module := &testModule{
		loadEntered: make(chan struct{}),
		releaseLoad: make(chan struct{}),
	}
	service := NewRuntime(rpc.NewRegistry(), newServiceStore(t), NewManager[*wrapperspb.Int64Value](module))

	firstResult := make(chan activateResult, 1)
	go func() {
		firstResult <- activateForTest(ctx, service, 2001)
	}()
	select {
	case <-module.loadEntered:
	case <-time.After(time.Second):
		t.Fatal("leader Activate did not enter OnLoad")
	}

	const waiters = 6
	results := make(chan activateResult, waiters+1)
	for range waiters {
		go func() {
			results <- activateForTest(ctx, service, 2001)
		}()
	}
	select {
	case result := <-results:
		t.Fatalf("waiter returned before first activation finished: %#v", result)
	case <-time.After(20 * time.Millisecond):
	}

	close(module.releaseLoad)
	results <- <-firstResult

	var firstRoot *Root[*wrapperspb.Int64Value]
	for i := 0; i < waiters+1; i++ {
		result := <-results
		if result.err != nil {
			t.Fatalf("Activate result %d error = %v", i, result.err)
		}
		if !result.isNew {
			t.Fatalf("Activate result %d isNew = false, want shared first result", i)
		}
		if firstRoot == nil {
			firstRoot = result.root
			continue
		}
		if result.root != firstRoot {
			t.Fatalf("Activate result %d root pointer differs", i)
		}
	}
	if got := module.countCall("load"); got != 1 {
		t.Fatalf("load hook count = %d, want 1", got)
	}
	if got := module.countCall("activate"); got != 1 {
		t.Fatalf("activate hook count = %d, want 1", got)
	}
}

func TestActivateExistingRootRunsActivateOnly(t *testing.T) {
	ctx := context.Background()
	setupServiceMemoryDB(t)
	module := &testModule{}
	service := NewRuntime(rpc.NewRegistry(), newServiceStore(t), NewManager[*wrapperspb.Int64Value](module))

	first, _, err := service.Activate(ctx, 2002)
	if err != nil {
		t.Fatalf("first Activate error = %v", err)
	}
	second, isNew, err := service.Activate(ctx, 2002)
	if err != nil {
		t.Fatalf("second Activate error = %v", err)
	}
	if isNew {
		t.Fatal("second isNew = true, want false")
	}
	if first != second {
		t.Fatal("second Activate returned a different root")
	}
	want := []string{"load", "activate", "activate"}
	if got := module.callsSnapshot(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

func TestActivateHookFailureDoesNotMarkRootActiveAndCanRetry(t *testing.T) {
	ctx := context.Background()
	setupServiceMemoryDB(t)
	module := &testModule{failActivate: true}
	service := NewRuntime(rpc.NewRegistry(), newServiceStore(t), NewManager[*wrapperspb.Int64Value](module))

	if _, _, err := service.Activate(ctx, 1002); err == nil {
		t.Fatal("Activate error = nil, want hook failure")
	}
	if _, ok := service.GetRoot(1002); ok {
		t.Fatal("root 1002 is active after failed activate")
	}
	module.setFailActivate(false)
	owner, isNew, err := service.Activate(ctx, 1002)
	if err != nil {
		t.Fatalf("retry Activate error = %v", err)
	}
	if owner == nil {
		t.Fatal("retry Activate root = nil")
	}
	if !isNew {
		t.Fatal("retry Activate isNew = false, want true")
	}
	if _, ok := service.GetRoot(1002); !ok {
		t.Fatal("root 1002 is not active after retry")
	}
}

func TestNewRuntimeBindsModuleContext(t *testing.T) {
	ctx := context.Background()
	setupServiceMemoryDB(t)
	reg := rpc.NewRegistry()
	module := &testModule{}
	service := NewRuntime(reg, newServiceStore(t), NewManager[*wrapperspb.Int64Value](module))

	if !module.moduleCtx.HasRootLookup() {
		t.Fatal("module context root lookup is missing")
	}
	if module.moduleCtx.Registry() != reg {
		t.Fatal("module context registry is not bound")
	}
	owner, _, err := service.Activate(ctx, 1003)
	if err != nil {
		t.Fatalf("Activate error = %v", err)
	}
	got, ok := module.moduleCtx.GetRoot(1003)
	if !ok || got != owner {
		t.Fatal("module context cannot query active root after activate")
	}
}

func TestModuleContextBindErrorFailsFast(t *testing.T) {
	ctx := context.Background()
	setupServiceMemoryDB(t)
	module := &testModule{bindErr: fmt.Errorf("bind failed")}
	service := NewRuntime(rpc.NewRegistry(), newServiceStore(t), NewManager[*wrapperspb.Int64Value](module))

	if err := service.RegisterRPC(); err == nil || !strings.Contains(err.Error(), "bind failed") {
		t.Fatalf("RegisterRPC error = %v, want bind failed", err)
	}
	if _, _, err := service.Activate(ctx, 1004); err == nil || !strings.Contains(err.Error(), "bind failed") {
		t.Fatalf("Activate error = %v, want bind failed", err)
	}
	if _, ok := service.GetRoot(1004); ok {
		t.Fatal("root 1004 is active after module context bind failure")
	}
}

func activateForTest(ctx context.Context, service *Runtime[*wrapperspb.Int64Value], rootID int64) activateResult {
	root, isNew, err := service.Activate(ctx, rootID)
	return activateResult{root: root, isNew: isNew, err: err}
}

type activateResult struct {
	root  *Root[*wrapperspb.Int64Value]
	isNew bool
	err   error
}

func setupServiceMemoryDB(t *testing.T) {
	t.Helper()
	rdb.SetDefaultDB(rdb.MustNewMemoryDB())
	t.Cleanup(func() {
		_ = rdb.CloseDefaultDB(context.Background())
	})
}

func newServiceStore(t *testing.T) *Store[*wrapperspb.Int64Value] {
	t.Helper()
	store, err := NewStore(StoreOptions[*wrapperspb.Int64Value]{
		Key: func(rootID int64) string {
			return "root:" + strconv.FormatInt(rootID, 10)
		},
		New: func(rootID int64) *wrapperspb.Int64Value {
			return wrapperspb.Int64(rootID)
		},
	})
	if err != nil {
		t.Fatalf("NewStore error = %v", err)
	}
	return store
}

type testModule struct {
	mu           sync.Mutex
	calls        []string
	failActivate bool
	bindErr      error
	moduleCtx    ModuleContext[*wrapperspb.Int64Value]
	loadEntered  chan struct{}
	releaseLoad  chan struct{}
	loadOnce     sync.Once
}

func (m *testModule) Descriptor() base.ModuleDescriptor {
	return base.ModuleDescriptor{ID: "test", Kind: base.ModuleKindRoot}
}

func (m *testModule) BindModuleContext(moduleCtx ModuleContext[*wrapperspb.Int64Value]) error {
	if m.bindErr != nil {
		return m.bindErr
	}
	m.moduleCtx = moduleCtx
	return nil
}

func (m *testModule) OnLoad(context.Context, *Root[*wrapperspb.Int64Value]) error {
	m.append("load")
	m.mu.Lock()
	loadEntered := m.loadEntered
	releaseLoad := m.releaseLoad
	m.mu.Unlock()
	if loadEntered != nil {
		m.loadOnce.Do(func() {
			close(loadEntered)
		})
	}
	if releaseLoad != nil {
		<-releaseLoad
	}
	return nil
}

func (m *testModule) OnActivate(context.Context, *Root[*wrapperspb.Int64Value]) error {
	m.append("activate")
	m.mu.Lock()
	fail := m.failActivate
	m.mu.Unlock()
	if fail {
		return fmt.Errorf("activate failed")
	}
	return nil
}

func (m *testModule) RegisterRPC(*rpc.Registry) error {
	return nil
}

func (m *testModule) setFailActivate(value bool) {
	m.mu.Lock()
	m.failActivate = value
	m.mu.Unlock()
}

func (m *testModule) append(value string) {
	m.mu.Lock()
	m.calls = append(m.calls, value)
	m.mu.Unlock()
}

func (m *testModule) callsSnapshot() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.calls...)
}

func (m *testModule) countCall(value string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, call := range m.calls {
		if call == value {
			count++
		}
	}
	return count
}
