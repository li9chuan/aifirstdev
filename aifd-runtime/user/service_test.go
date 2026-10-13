package user

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	base "github.com/li9chuan/aifirstdev/aifd-contracts"
	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestLoadNeverCreatesAndCachedLoadDoesNotRepeatHooks(t *testing.T) {
	ctx := context.Background()
	setupMemoryDB(t)
	module := &testModule{}
	rt := NewRuntime(rpc.NewRegistry(), newInt64Store(t), NewManager[*wrapperspb.Int64Value](module))
	if _, err := rt.LoadUser(ctx, 1001); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing load: %v", err)
	}
	if _, ok := rt.GetUser(1001); ok {
		t.Fatal("missing user was cached")
	}
	owner, err := rt.CreateUser(ctx, 1001)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.CreateUser(ctx, 1001); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate create: %v", err)
	}
	loaded, err := rt.LoadUser(ctx, 1001)
	if err != nil || loaded != owner {
		t.Fatalf("cached load: %p %v", loaded, err)
	}
	if got := module.loadCount(); got != 1 {
		t.Fatalf("load hooks = %d", got)
	}
	if module.moduleCtx.Runtime() != rt || module.moduleCtx.Registry() != rt.Registry() {
		t.Fatal("module facade not bound")
	}
}

func TestConcurrentLoadMergesAndCancelledWaiterDoesNotInvalidateOwner(t *testing.T) {
	ctx := context.Background()
	setupMemoryDB(t)
	store := newInt64Store(t)
	if _, err := store.Create(ctx, 2001); err != nil {
		t.Fatal(err)
	}
	if err := rdb.FlushDefaultDB(ctx); err != nil {
		t.Fatal(err)
	}
	module := &testModule{entered: make(chan struct{}), release: make(chan struct{})}
	rt := NewRuntime(rpc.NewRegistry(), store, NewManager[*wrapperspb.Int64Value](module))
	type result struct {
		owner *User[*wrapperspb.Int64Value]
		err   error
	}
	results := make(chan result, 9)
	go func() { owner, err := rt.LoadUser(ctx, 2001); results <- result{owner, err} }()
	select {
	case <-module.entered:
	case <-time.After(time.Second):
		t.Fatal("load did not begin")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := rt.LoadUser(cancelled, 2001); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter: %v", err)
	}
	for range 8 {
		go func() { owner, err := rt.LoadUser(ctx, 2001); results <- result{owner, err} }()
	}
	close(module.release)
	var first *User[*wrapperspb.Int64Value]
	for range 9 {
		select {
		case got := <-results:
			if got.err != nil {
				t.Fatal(got.err)
			}
			if first == nil {
				first = got.owner
			} else if first != got.owner {
				t.Fatal("multiple user objects published")
			}
		case <-time.After(time.Second):
			t.Fatal("load waiter did not settle")
		}
	}
	if module.loadCount() != 1 {
		t.Fatal("OnLoad repeated")
	}
}

func TestLoadHookFailureAndPanicCanRetry(t *testing.T) {
	ctx := context.Background()
	setupMemoryDB(t)
	store := newInt64Store(t)
	if _, err := store.Create(ctx, 2002); err != nil {
		t.Fatal(err)
	}
	if err := rdb.FlushDefaultDB(ctx); err != nil {
		t.Fatal(err)
	}
	module := &testModule{fail: true}
	rt := NewRuntime(rpc.NewRegistry(), store, NewManager[*wrapperspb.Int64Value](module))
	if _, err := rt.LoadUser(ctx, 2002); err == nil {
		t.Fatal("failing hook was accepted")
	}
	if _, ok := rt.GetUser(2002); ok {
		t.Fatal("failed user published")
	}
	module.mu.Lock()
	module.fail = false
	module.panicLoad = true
	module.mu.Unlock()
	if _, err := rt.LoadUser(ctx, 2002); err == nil {
		t.Fatal("panic was accepted")
	}
	module.mu.Lock()
	module.panicLoad = false
	module.mu.Unlock()
	if _, err := rt.LoadUser(ctx, 2002); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if _, ok := rt.GetUser(2002); !ok {
		t.Fatal("retry did not publish")
	}
}

func TestCloseRejectsLoadsAndModuleBindingFailsFast(t *testing.T) {
	ctx := context.Background()
	setupMemoryDB(t)
	bad := &testModule{bindErr: fmt.Errorf("bind rejected")}
	rt := NewRuntime(rpc.NewRegistry(), newInt64Store(t), NewManager[*wrapperspb.Int64Value](bad))
	if err := rt.RegisterRPC(); err == nil {
		t.Fatal("binding failure hidden")
	}
	if _, err := rt.LoadUser(ctx, 2003); err == nil {
		t.Fatal("binding failure hidden by load")
	}
	rt = NewRuntime(rpc.NewRegistry(), newInt64Store(t), nil)
	if err := rt.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.LoadUser(ctx, 2003); !errors.Is(err, ErrClosed) {
		t.Fatalf("load after close: %v", err)
	}
	if _, err := rt.CreateUser(ctx, 2003); !errors.Is(err, ErrClosed) {
		t.Fatalf("create after close: %v", err)
	}
}

type testModule struct {
	mu        sync.Mutex
	count     int
	fail      bool
	panicLoad bool
	entered   chan struct{}
	release   chan struct{}
	bindErr   error
	moduleCtx ModuleContext[*wrapperspb.Int64Value]
}

func (m *testModule) Descriptor() base.ModuleDescriptor {
	return base.ModuleDescriptor{ID: "test.user"}
}
func (m *testModule) RegisterRPC(*rpc.Registry) error { return nil }
func (m *testModule) BindModuleContext(c ModuleContext[*wrapperspb.Int64Value]) error {
	m.moduleCtx = c
	return m.bindErr
}
func (m *testModule) OnLoad(context.Context, *User[*wrapperspb.Int64Value]) error {
	m.mu.Lock()
	m.count++
	fail, panicLoad := m.fail, m.panicLoad
	entered, release := m.entered, m.release
	m.mu.Unlock()
	if entered != nil {
		close(entered)
		<-release
	}
	if fail {
		return fmt.Errorf("load failed")
	}
	if panicLoad {
		panic("load panic")
	}
	return nil
}
func (m *testModule) loadCount() int { m.mu.Lock(); defer m.mu.Unlock(); return m.count }
