package user

import (
	"context"
	"errors"
	"fmt"
	proxyruntime "github.com/li9chuan/aifirstdev/aifd-proxy/runtime"
	"sync"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	contracts "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	"google.golang.org/protobuf/proto"
)

// ErrClosed rejects new work after the user runtime has closed.
var ErrClosed = errors.New("user runtime is closed")

// Runtime owns the single UID-to-user cache and user module lifecycle.
// Loading is independent of client connection lifetime and never implicitly creates.
type Runtime[T proto.Message] struct {
	mu               sync.Mutex
	users            map[int64]*User[T]
	loading          map[int64]*load[T]
	modules          *Manager[T]
	reg              *rpc.Registry
	store            *Store[T]
	bus              contracts.Bus
	moduleContextErr error
	closed           bool
	connections      *proxyruntime.ClientSessions
	defaultHeartbeat bool
}

type load[T proto.Message] struct {
	done   chan struct{}
	user   *User[T]
	err    error
	create bool
}

type options struct {
	bus              contracts.Bus
	sessions         proxyruntime.ClientSessionOptions
	defaultHeartbeat bool
}

// Option configures runtime-owned capabilities.
type Option func(*options)

// WithBus binds RPC capabilities used by internally constructed sessions.
func WithBus(bus contracts.Bus) Option { return func(o *options) { o.bus = bus } }

// WithClientSessions configures per-connection lease timing and optional callbacks.
func WithClientSessions(config proxyruntime.ClientSessionOptions) Option {
	return func(o *options) { o.sessions = config }
}

// WithDefaultHeartbeat enables the framework heartbeat for applications without a business one.
func WithDefaultHeartbeat() Option { return func(o *options) { o.defaultHeartbeat = true } }

// NewRuntime creates a typed user runtime. Registry and module manager default locally.
func NewRuntime[T proto.Message](reg *rpc.Registry, store *Store[T], modules *Manager[T], opts ...Option) *Runtime[T] {
	if reg == nil {
		reg = rpc.NewRegistry()
	}
	if modules == nil {
		modules = NewManager[T]()
	}
	var config options
	for _, option := range opts {
		if option != nil {
			option(&config)
		}
	}
	r := &Runtime[T]{users: make(map[int64]*User[T]), loading: make(map[int64]*load[T]), modules: modules, reg: reg, store: store, bus: config.bus}
	r.connections = proxyruntime.NewClientSessions(config.sessions)
	r.defaultHeartbeat = config.defaultHeartbeat
	r.moduleContextErr = modules.BindModuleContext(NewModuleContext[T](r, reg))
	return r
}

// RegisterRPC installs routes owned by the configured business modules.
func (r *Runtime[T]) RegisterRPC() error {
	if err := r.check(); err != nil {
		return err
	}
	if err := r.connections.RegisterRPC(r.reg); err != nil {
		return err
	}
	if r.defaultHeartbeat {
		if err := r.connections.RegisterHeartbeat(r.reg); err != nil {
			return err
		}
	}
	return r.modules.RegisterRPC(r.reg)
}

// RenewConnection renews the authenticated connection attached to this RPC only.
func (r *Runtime[T]) RenewConnection(s *rpc.Session) error { return r.connections.RenewCaller(s) }

// OnlineConnections reads ephemeral liveness without loading business data.
func (r *Runtime[T]) OnlineConnections(uid int64) int { return r.connections.Count(uid) }

// Run is explicitly wired into the application's service lifecycle.
func (r *Runtime[T]) Run(ctx context.Context) error { return r.connections.Run(ctx) }

// LoadUser returns an existing user, merging concurrent loads of the same UID.
// It never creates a document, activates a connection, or repeats OnLoad for a cached user.
func (r *Runtime[T]) LoadUser(ctx context.Context, uid int64) (*User[T], error) {
	return r.acquire(ctx, uid, false)
}

// CreateUser explicitly initializes a new user. An existing user is an error.
func (r *Runtime[T]) CreateUser(ctx context.Context, uid int64) (*User[T], error) {
	return r.acquire(ctx, uid, true)
}

// GetUser is a pure cache lookup with no storage I/O or lifecycle side effects.
func (r *Runtime[T]) GetUser(uid int64) (*User[T], bool) {
	if r == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	owner, ok := r.users[uid]
	return owner, ok && !r.closed
}

// Registry returns the runtime-owned business registry.
func (r *Runtime[T]) Registry() *rpc.Registry { return r.reg }

// NewSession creates a trusted internal invocation only when business needs RPC abilities.
// It has no client connection reference, inbox, or response channel.
func (r *Runtime[T]) NewSession(ctx context.Context, uid int64) *rpc.Session {
	r.mu.Lock()
	bus := r.bus
	if r.closed {
		bus = nil
	}
	r.mu.Unlock()
	return rpc.NewSession(ctx, uid, bus)
}

// BindBus is startup wiring for services that open their bus after constructing
// modules. A bound runtime cannot be rebound to another service identity.
func (r *Runtime[T]) BindBus(bus contracts.Bus) error {
	if bus == nil {
		return fmt.Errorf("user runtime RPC bus is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	if r.bus != nil {
		return fmt.Errorf("user runtime RPC bus is already bound")
	}
	r.bus = bus
	return nil
}

// Close rejects new loads and waits for admitted initialization. Persistence is owned
// by the application's RDB lifecycle; this method does not close a shared database.
func (r *Runtime[T]) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	r.closed = true
	pending := make([]*load[T], 0, len(r.loading))
	for _, current := range r.loading {
		pending = append(pending, current)
	}
	r.mu.Unlock()
	for _, current := range pending {
		select {
		case <-current.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return r.connections.Close(ctx)
}

func (r *Runtime[T]) acquire(ctx context.Context, uid int64, create bool) (*User[T], error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, fmt.Errorf("user context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if uid <= 0 {
		return nil, fmt.Errorf("user UID must be positive")
	}
	for {
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return nil, ErrClosed
		}
		if owner := r.users[uid]; owner != nil {
			r.mu.Unlock()
			if create {
				return nil, ErrAlreadyExists
			}
			return owner, nil
		}
		if current := r.loading[uid]; current != nil {
			r.mu.Unlock()
			select {
			case <-current.done:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if create && !current.create {
				if errors.Is(current.err, ErrNotFound) {
					continue
				}
				if current.err == nil {
					return nil, ErrAlreadyExists
				}
			}
			if create && current.create && current.err == nil {
				return nil, ErrAlreadyExists
			}
			return current.user, current.err
		}
		current := &load[T]{done: make(chan struct{}), create: create}
		r.loading[uid] = current
		r.mu.Unlock()

		owner, err := r.initialize(ctx, uid, create)
		r.mu.Lock()
		if err == nil && r.closed {
			err = ErrClosed
			owner = nil
		}
		current.user, current.err = owner, err
		if err == nil {
			r.users[uid] = owner
		}
		delete(r.loading, uid)
		close(current.done)
		r.mu.Unlock()
		return owner, err
	}
}

func (r *Runtime[T]) initialize(ctx context.Context, uid int64, create bool) (owner *User[T], err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			owner = nil
			err = fmt.Errorf("user initialize UID=%d panic: %v", uid, recovered)
		}
	}()
	if r.store == nil {
		return nil, fmt.Errorf("user store is nil")
	}
	if create {
		owner, err = r.store.Create(ctx, uid)
	} else {
		owner, err = r.store.Load(ctx, uid)
	}
	if err != nil {
		return nil, fmt.Errorf("user document UID=%d: %w", uid, err)
	}
	if err = r.modules.OnLoad(ctx, owner); err != nil {
		return nil, fmt.Errorf("user load hook UID=%d: %w", uid, err)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return owner, nil
}

func (r *Runtime[T]) check() error {
	if r == nil {
		return fmt.Errorf("user runtime is nil")
	}
	if r.moduleContextErr != nil {
		return fmt.Errorf("user module context bind: %w", r.moduleContextErr)
	}
	return nil
}
