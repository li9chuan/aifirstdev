package robot

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestOnAttachDoesNotRegisterCounterReplyPush(t *testing.T) {
	module := NewModule(ModuleOptions{})
	agent := &testAgent{}

	if err := module.OnAttach(context.Background(), agent); err != nil {
		t.Fatalf("OnAttach error = %v", err)
	}
	if agent.onCount != 0 {
		t.Fatalf("On registrations = %d, want 0", agent.onCount)
	}
}

type testAgent struct {
	onCount int
}

func (a *testAgent) ID() string {
	return "test"
}

func (a *testAgent) UID() int64 {
	return 1
}

func (a *testAgent) Account() string {
	return "test"
}

func (a *testAgent) IsLoggedIn() bool {
	return true
}

func (a *testAgent) Call(proto.Message, ...any) error {
	return nil
}

func (a *testAgent) On(any) error {
	a.onCount++
	return nil
}
