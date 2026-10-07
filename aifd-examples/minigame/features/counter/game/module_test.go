package game

import (
	"testing"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
)

func TestRegisterRPCRequiresModuleContext(t *testing.T) {
	module := NewModule(Options{})

	if err := module.RegisterRPC(rpc.NewRegistry()); err == nil {
		t.Fatal("RegisterRPC error = nil, want missing module context")
	}
}
