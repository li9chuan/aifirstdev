package user

import (
	"context"
	"fmt"

	base "github.com/li9chuan/aifirstdev/aifd-contracts"
	"github.com/li9chuan/aifirstdev/aifd-log"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	"google.golang.org/protobuf/proto"
)

// Module owns one slice of the primary proto and its runtime behavior.
type Module[T proto.Message] interface {
	Descriptor() base.ModuleDescriptor
	OnLoad(context.Context, *User[T]) error
	RegisterRPC(*rpc.Registry) error
}

// Manager runs user modules in a deterministic order.
type Manager[T proto.Message] struct {
	modules []Module[T]
}

// NewManager creates a user module manager and copies the input order.
func NewManager[T proto.Message](modules ...Module[T]) *Manager[T] {
	return &Manager[T]{modules: append([]Module[T](nil), modules...)}
}

// Register appends a module to the deterministic root execution order.
func (m *Manager[T]) Register(module Module[T]) {
	m.modules = append(m.modules, module)
}

// BindModuleContext injects runtime context into user modules that ask for it.
func (m *Manager[T]) BindModuleContext(moduleCtx ModuleContext[T]) error {
	for _, module := range m.modules {
		contextModule, ok := module.(ContextModule[T])
		if !ok {
			continue
		}
		if err := contextModule.BindModuleContext(moduleCtx); err != nil {
			return fmt.Errorf("user module %s bind context: %w", module.Descriptor().ID, err)
		}
	}
	return nil
}

// OnLoad runs module load hooks before the user is published.
func (m *Manager[T]) OnLoad(ctx context.Context, owner *User[T]) error {
	for _, module := range m.modules {
		if err := module.OnLoad(ctx, owner); err != nil {
			return err
		}
	}
	return nil
}

// RegisterRPC allows modules to bind business routes to the shared registry.
func (m *Manager[T]) RegisterRPC(reg *rpc.Registry) error {
	return m.registerRPC(reg, nil)
}

func (m *Manager[T]) registerRPC(reg *rpc.Registry, validateHeader func(...logging.StatSchema) error) error {
	var events []logging.StatSchema
	for _, module := range m.modules {
		if provider, ok := module.(logging.StatEventProvider); ok {
			events = append(events, provider.StatEvents()...)
		}
	}
	if validateHeader != nil {
		if err := validateHeader(events...); err != nil {
			return fmt.Errorf("user stat header: %w", err)
		}
	}
	if err := logging.RegisterStatEvents(events...); err != nil {
		return fmt.Errorf("user stat events: %w", err)
	}
	for _, module := range m.modules {
		if err := module.RegisterRPC(reg); err != nil {
			return err
		}
	}
	return nil
}
