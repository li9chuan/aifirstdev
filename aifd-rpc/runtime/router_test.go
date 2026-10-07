package runtime

import (
	"context"
	"testing"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	"google.golang.org/protobuf/proto"
)

func TestRouterDispatch(t *testing.T) {
	router := NewRouter()
	called := false
	err := router.Register(rpc.Route{
		Name: "MceTest",
		Kind: rpc.RouteKindClient,
		Handler: func(context.Context, rpc.Session, rpc.Message) error {
			called = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Register error = %v", err)
	}

	if err := router.Dispatch(context.Background(), noopSession{}, rpc.Message{Name: "MceTest"}); err != nil {
		t.Fatalf("Dispatch error = %v", err)
	}
	if !called {
		t.Fatal("handler was not called")
	}
}

func TestRouterRejectsDuplicate(t *testing.T) {
	router := NewRouter()
	route := rpc.Route{Name: "MceTest", Handler: func(context.Context, rpc.Session, rpc.Message) error { return nil }}
	if err := router.Register(route); err != nil {
		t.Fatalf("first Register error = %v", err)
	}
	if err := router.Register(route); err == nil {
		t.Fatal("second Register error = nil, want duplicate error")
	}
}

type noopSession struct{}

func (noopSession) UID() int64                                     { return 0 }
func (noopSession) Topic() string                                  { return "" }
func (noopSession) Inbox() string                                  { return "" }
func (noopSession) ClientInbox() int                               { return 0 }
func (noopSession) Respond(context.Context, rpc.Message) error     { return nil }
func (noopSession) RespondPB(context.Context, proto.Message) error { return nil }
func (noopSession) Push(context.Context, int64, rpc.Message) error { return nil }
func (noopSession) PushPB(context.Context, int64, proto.Message) error {
	return nil
}
