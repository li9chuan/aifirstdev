package service

import (
	"context"
	"fmt"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
)

// Service orchestrates service-level modules.
type Service struct {
	modules []Module
	reg     *rpc.Registry
}

// NewService creates a lifecycle service and copies modules to keep ownership stable.
func NewService(reg *rpc.Registry, modules ...Module) *Service {
	if reg == nil {
		reg = rpc.NewRegistry()
	}
	return &Service{reg: reg, modules: append([]Module(nil), modules...)}
}

// Recover runs module recovery before RPC registration or background work starts.
func (s *Service) Recover(ctx context.Context) error {
	for _, module := range s.modules {
		if err := module.Recover(ctx); err != nil {
			return fmt.Errorf("service module %s recover: %w", module.Descriptor().ID, err)
		}
	}
	return nil
}

// RegisterRPC allows each module to bind business routes to the service registry.
func (s *Service) RegisterRPC() error {
	for _, module := range s.modules {
		if err := module.RegisterRPC(s.reg); err != nil {
			return fmt.Errorf("service module %s register rpc: %w", module.Descriptor().ID, err)
		}
	}
	return nil
}

// Start begins each recovered module's runtime work.
func (s *Service) Start(ctx context.Context) error {
	for _, module := range s.modules {
		if err := module.Start(ctx); err != nil {
			return fmt.Errorf("service module %s start: %w", module.Descriptor().ID, err)
		}
	}
	return nil
}

// Close stops modules in reverse registration order.
func (s *Service) Close(ctx context.Context) error {
	for i := len(s.modules) - 1; i >= 0; i-- {
		if err := s.modules[i].Close(ctx); err != nil {
			return fmt.Errorf("service module %s close: %w", s.modules[i].Descriptor().ID, err)
		}
	}
	return nil
}
