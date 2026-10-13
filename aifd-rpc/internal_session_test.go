package rpc

import (
	"context"
	"strings"
	"testing"

	local "github.com/li9chuan/aifirstdev/aifd-rpc/adapters/local"
	contracts "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestInternalSessionHasRPCAbilitiesWithoutReplyChannel(t *testing.T) {
	ctx := context.Background()
	bus := local.NewBus(local.Config{})
	defer bus.Close(ctx)
	s := NewSession(ctx, 42, bus)
	if s.UID() != 42 || s.Context() != ctx {
		t.Fatal("internal invocation lost context or UID")
	}
	if result := s.Response(wrapperspb.String("ack")); result.OK() || !strings.Contains(result.Message(), "responder") {
		t.Fatalf("internal response = %#v", result)
	}
	if result := s.Push(wrapperspb.String("event")); !result.OK() {
		t.Fatal(result.String())
	}
	pushes := bus.Pushes()
	if len(pushes) != 1 || pushes[0].UID != 42 || pushes[0].Message.Header[contracts.HeaderClientInbox] != "" {
		t.Fatalf("internal push = %#v", pushes)
	}
}
