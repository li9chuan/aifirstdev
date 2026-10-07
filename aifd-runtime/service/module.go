package service

import (
	"context"

	base "github.com/li9chuan/aifirstdev/aifd-contracts"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
)

// Module owns service-level recovery, RPC registration, startup, and shutdown.
type Module interface {
	Descriptor() base.ModuleDescriptor
	Recover(context.Context) error
	RegisterRPC(*rpc.Registry) error
	Start(context.Context) error
	Close(context.Context) error
}
