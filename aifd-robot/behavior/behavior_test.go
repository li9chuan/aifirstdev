package behavior

import (
	"context"
	"testing"

	robot "github.com/li9chuan/aifirstdev/aifd-robot/contracts"
)

func TestSequenceRunsRegisteredAction(t *testing.T) {
	registry := NewRegistry()
	called := 0
	if err := registry.Register(robot.ActionRegistration{
		Name: "ok",
		Factory: func(robot.Settings) robot.Action {
			return robot.ActionFunc(func(context.Context, robot.Agent) robot.Status {
				called++
				return robot.StatusSuccess
			})
		},
	}); err != nil {
		t.Fatalf("Register error = %v", err)
	}
	root, err := Build(robot.BehaviorSpec{
		Type: "sequence",
		Children: []robot.BehaviorSpec{
			{Name: "ok"},
			{Name: "ok"},
		},
	}, registry)
	if err != nil {
		t.Fatalf("Build error = %v", err)
	}
	if status := root.Tick(context.Background(), nil); status != robot.StatusSuccess {
		t.Fatalf("status = %s, want success", status)
	}
	if called != 2 {
		t.Fatalf("called = %d, want 2", called)
	}
}
