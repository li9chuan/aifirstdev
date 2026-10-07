package root

import (
	"context"
	"fmt"
	"sync"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	"google.golang.org/protobuf/proto"
)

// Runtime hosts active roots, root modules, and the root store.
type Runtime[T proto.Message] struct {
	mu               sync.Mutex
	roots            map[int64]*Root[T]
	activating       map[int64]*activation[T]
	modules          *Manager[T]
	reg              *rpc.Registry
	store            *Store[T]
	moduleContextErr error
}

type activation[T proto.Message] struct {
	done  chan struct{}
	root  *Root[T]
	isNew bool
	err   error
}

// NewRuntime creates a root runtime with default registry and module manager when omitted.
func NewRuntime[T proto.Message](reg *rpc.Registry, store *Store[T], modules *Manager[T]) *Runtime[T] {
	if reg == nil {
		reg = rpc.NewRegistry()
	}
	if modules == nil {
		modules = NewManager[T]()
	}
	runtime := &Runtime[T]{
		roots:      make(map[int64]*Root[T]),
		activating: make(map[int64]*activation[T]),
		modules:    modules,
		reg:        reg,
		store:      store,
	}
	runtime.moduleContextErr = modules.BindModuleContext(NewModuleContext[T](runtime, reg))
	return runtime
}

// RegisterRPC allows root modules to bind business routes to the registry.
func (r *Runtime[T]) RegisterRPC() error {
	if err := r.moduleContextError(); err != nil {
		return err
	}
	return r.modules.RegisterRPC(r.reg)
}

// Activate loads or creates a root, runs load and activation hooks, and marks it active.
//
// GetRoot is a pure active-map lookup. Activate is the only API that loads,
// creates, and runs lifecycle hooks.
func (r *Runtime[T]) Activate(ctx context.Context, rootID int64) (*Root[T], bool, error) {
	if err := r.moduleContextError(); err != nil {
		return nil, false, err
	}
	if rootID <= 0 {
		return nil, false, fmt.Errorf("root id must be positive")
	}
	current, active, leader := r.startActivation(rootID)
	if active != nil {
		if err := r.modules.OnActivate(ctx, active); err != nil {
			return nil, false, fmt.Errorf("root activate rootID=%d: %w", rootID, err)
		}
		return active, false, nil
	}
	if !leader {
		return waitActivation(ctx, current)
	}
	root, isNew, err := r.activateFirst(ctx, rootID)
	r.finishActivation(rootID, current, root, isNew, err)
	return root, isNew, err
}

// GetRoot returns an active root without loading, creating, or running hooks.
func (r *Runtime[T]) GetRoot(rootID int64) (*Root[T], bool) {
	return r.getActiveRoot(rootID)
}

// Registry returns the root runtime RPC registry.
func (r *Runtime[T]) Registry() *rpc.Registry {
	return r.reg
}

func (r *Runtime[T]) getActiveRoot(rootID int64) (*Root[T], bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	root, ok := r.roots[rootID]
	return root, ok
}

func (r *Runtime[T]) startActivation(rootID int64) (*activation[T], *Root[T], bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if root, ok := r.roots[rootID]; ok {
		return nil, root, false
	}
	if current, ok := r.activating[rootID]; ok {
		return current, nil, false
	}
	current := &activation[T]{done: make(chan struct{})}
	r.activating[rootID] = current
	return current, nil, true
}

func (r *Runtime[T]) activateFirst(ctx context.Context, rootID int64) (*Root[T], bool, error) {
	if r.store == nil {
		return nil, false, fmt.Errorf("root store is nil")
	}
	root, isNew, err := r.store.LoadOrNew(ctx, rootID)
	if err != nil {
		return nil, false, fmt.Errorf("root load rootID=%d: %w", rootID, err)
	}
	if err := r.modules.OnLoad(ctx, root); err != nil {
		return nil, false, fmt.Errorf("root load hook rootID=%d: %w", rootID, err)
	}
	if err := r.modules.OnActivate(ctx, root); err != nil {
		return nil, false, fmt.Errorf("root activate rootID=%d: %w", rootID, err)
	}
	return root, isNew, nil
}

func (r *Runtime[T]) finishActivation(rootID int64, current *activation[T], root *Root[T], isNew bool, err error) {
	r.mu.Lock()
	current.root = root
	current.isNew = isNew
	current.err = err
	if err == nil {
		r.roots[rootID] = root
	}
	delete(r.activating, rootID)
	close(current.done)
	r.mu.Unlock()
}

func waitActivation[T proto.Message](ctx context.Context, current *activation[T]) (*Root[T], bool, error) {
	select {
	case <-current.done:
		return current.root, current.isNew, current.err
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}
}

func (r *Runtime[T]) moduleContextError() error {
	if r == nil {
		return fmt.Errorf("root runtime is nil")
	}
	if r.moduleContextErr != nil {
		return fmt.Errorf("root module context bind: %w", r.moduleContextErr)
	}
	return nil
}
