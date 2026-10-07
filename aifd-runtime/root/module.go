package root

import (
	"context"
	"fmt"

	base "github.com/li9chuan/aifirstdev/aifd-contracts"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	"google.golang.org/protobuf/proto"
)

// Module owns one slice of the root proto and its runtime behavior.
type Module[T proto.Message] interface {
	Descriptor() base.ModuleDescriptor
	OnLoad(context.Context, *Root[T]) error
	OnActivate(context.Context, *Root[T]) error
	RegisterRPC(*rpc.Registry) error
}

// Manager runs root modules in a deterministic order.
type Manager[T proto.Message] struct {
	modules []Module[T]
}

// NewManager creates a root module manager and copies the input order.
func NewManager[T proto.Message](modules ...Module[T]) *Manager[T] {
	return &Manager[T]{modules: append([]Module[T](nil), modules...)}
}

// Register appends a module to the deterministic root execution order.
func (m *Manager[T]) Register(module Module[T]) {
	m.modules = append(m.modules, module)
}

// BindModuleContext injects runtime context into root modules that ask for it.
func (m *Manager[T]) BindModuleContext(moduleCtx ModuleContext[T]) error {
	for _, module := range m.modules {
		contextModule, ok := module.(ContextModule[T])
		if !ok {
			continue
		}
		if err := contextModule.BindModuleContext(moduleCtx); err != nil {
			return fmt.Errorf("root module %s bind context: %w", module.Descriptor().ID, err)
		}
	}
	return nil
}

// OnLoad runs module load hooks before the root activation event.
func (m *Manager[T]) OnLoad(ctx context.Context, root *Root[T]) error {
	for _, module := range m.modules {
		if err := module.OnLoad(ctx, root); err != nil {
			return err
		}
	}
	return nil
}

// OnActivate runs module activation hooks for a root.
func (m *Manager[T]) OnActivate(ctx context.Context, root *Root[T]) error {
	for _, module := range m.modules {
		if err := module.OnActivate(ctx, root); err != nil {
			return err
		}
	}
	return nil
}

// RegisterRPC allows modules to bind business routes to the shared registry.
func (m *Manager[T]) RegisterRPC(reg *rpc.Registry) error {
	for _, module := range m.modules {
		if err := module.RegisterRPC(reg); err != nil {
			return err
		}
	}
	return nil
}
