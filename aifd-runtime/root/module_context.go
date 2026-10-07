package root

import (
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	"google.golang.org/protobuf/proto"
)

// RootLookup exposes the runtime-owned active root lookup.
type RootLookup[T proto.Message] interface {
	GetRoot(rootID int64) (*Root[T], bool)
}

// ModuleContext provides the minimal runtime wiring for root modules.
type ModuleContext[T proto.Message] struct {
	roots RootLookup[T]
	reg   *rpc.Registry
}

// ContextModule is optionally implemented by root modules that need runtime context.
type ContextModule[T proto.Message] interface {
	BindModuleContext(ModuleContext[T]) error
}

// NewModuleContext creates root module runtime context.
func NewModuleContext[T proto.Message](roots RootLookup[T], reg *rpc.Registry) ModuleContext[T] {
	return ModuleContext[T]{roots: roots, reg: reg}
}

// GetRoot returns an active root without loading, creating, or running hooks.
func (c ModuleContext[T]) GetRoot(rootID int64) (*Root[T], bool) {
	if c.roots == nil {
		return nil, false
	}
	return c.roots.GetRoot(rootID)
}

// Registry returns the shared root runtime RPC registry.
func (c ModuleContext[T]) Registry() *rpc.Registry {
	return c.reg
}

// HasRootLookup reports whether the context includes the active root lookup.
func (c ModuleContext[T]) HasRootLookup() bool {
	return c.roots != nil
}
