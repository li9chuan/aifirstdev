package rpc

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestStartInstallsRoutesAndRunsLocalBus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg := NewRegistry()
	if err := Client(reg, "MceFacade", func(s *Session, req *wrapperspb.StringValue) {
		s.Response(wrapperspb.String(req.GetValue() + ":rsp"))
	}); err != nil {
		t.Fatalf("Client error = %v", err)
	}

	bus := NewLocalBus(LocalConfig{ServiceName: "facade-test", Buffer: 8, DefaultTimeout: time.Second})
	defer bus.Close(context.Background())
	if err := Start(ctx, bus, reg, RunnerOptions{Workers: 1, QueueSize: 4}); err != nil {
		t.Fatalf("Start error = %v", err)
	}

	var rsp wrapperspb.StringValue
	if err := bus.RequestPB(ctx, 1001, "MceFacade", wrapperspb.String("hello"), &rsp, RequestOptions{Timeout: time.Second}); err != nil {
		t.Fatalf("RequestPB error = %v", err)
	}
	if rsp.GetValue() != "hello:rsp" {
		t.Fatalf("rsp = %q, want hello:rsp", rsp.GetValue())
	}
}

func TestStartRunnerSurvivesStartContextCancelUntilBusClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reg := NewRegistry()
	if err := Client(reg, "MceDrainStart", func(s *Session, req *wrapperspb.StringValue) {
		s.Response(wrapperspb.String(req.GetValue() + ":rsp"))
	}); err != nil {
		t.Fatalf("Client error = %v", err)
	}

	bus := NewLocalBus(LocalConfig{ServiceName: "facade-drain-test", Buffer: 8, DefaultTimeout: time.Second})
	defer bus.Close(context.Background())
	if err := Start(ctx, bus, reg, RunnerOptions{Workers: 1, QueueSize: 4}); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	cancel()

	var rsp wrapperspb.StringValue
	if err := bus.RequestPB(context.Background(), 1001, "MceDrainStart", wrapperspb.String("hello"), &rsp, RequestOptions{Timeout: time.Second}); err != nil {
		t.Fatalf("RequestPB after start context cancel error = %v", err)
	}
	if rsp.GetValue() != "hello:rsp" {
		t.Fatalf("rsp = %q, want hello:rsp", rsp.GetValue())
	}
}

func TestStartInstallErrorDoesNotRunLocalBus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := NewLocalBus(LocalConfig{ServiceName: "facade-fail-test", Buffer: 8})
	defer bus.Close(context.Background())

	called := make(chan struct{}, 1)
	installed := NewRegistry()
	if err := Client(installed, "MceDuplicate", func(*Session) {
		called <- struct{}{}
	}); err != nil {
		t.Fatalf("Client installed error = %v", err)
	}
	if err := InstallContext(ctx, bus, installed); err != nil {
		t.Fatalf("InstallContext installed error = %v", err)
	}

	duplicate := NewRegistry()
	if err := Client(duplicate, "MceDuplicate", func(*Session) {}); err != nil {
		t.Fatalf("Client duplicate error = %v", err)
	}
	if err := Start(ctx, bus, duplicate, RunnerOptions{Workers: 1, QueueSize: 1}); err == nil {
		t.Fatal("Start error = nil, want duplicate route error")
	}
	if err := bus.TellPB(ctx, 1, "MceDuplicate", wrapperspb.String("noop")); err != nil {
		t.Fatalf("TellPB error = %v", err)
	}

	select {
	case <-called:
		t.Fatal("handler was called after failed Start")
	case <-time.After(50 * time.Millisecond):
	}
}
