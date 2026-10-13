package user

import (
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	"google.golang.org/protobuf/proto"
)

// UserLookup exposes the runtime-owned resident user lookup.
type UserLookup[T proto.Message] interface {
	GetUser(uid int64) (*User[T], bool)
}

// ModuleContext provides the minimal runtime wiring for user modules.
type ModuleContext[T proto.Message] struct {
	roots   UserLookup[T]
	reg     *rpc.Registry
	runtime *Runtime[T]
}

// ContextModule is optionally implemented by user modules that need runtime context.
type ContextModule[T proto.Message] interface {
	BindModuleContext(ModuleContext[T]) error
}

// NewModuleContext creates user module runtime context.
func NewModuleContext[T proto.Message](roots UserLookup[T], reg *rpc.Registry) ModuleContext[T] {
	runtime, _ := roots.(*Runtime[T])
	return ModuleContext[T]{roots: roots, reg: reg, runtime: runtime}
}

// Runtime exposes the typed user routing facade to modules during registration.
func (c ModuleContext[T]) Runtime() *Runtime[T] { return c.runtime }

// GetUser returns an resident user without loading, creating, or running hooks.
func (c ModuleContext[T]) GetUser(uid int64) (*User[T], bool) {
	if c.roots == nil {
		return nil, false
	}
	return c.roots.GetUser(uid)
}

// Registry returns the shared user runtime RPC registry.
func (c ModuleContext[T]) Registry() *rpc.Registry {
	return c.reg
}

// HasUserLookup reports whether the context includes the resident user lookup.
func (c ModuleContext[T]) HasUserLookup() bool {
	return c.roots != nil
}
