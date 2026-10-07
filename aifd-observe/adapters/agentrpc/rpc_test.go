package agentrpc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-observe/agent"
	"github.com/li9chuan/aifirstdev/aifd-observe/contracts"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc"
	rpccontracts "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestRegisterTailRoute(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.log")
	content := "" +
		`{"time":"2026-06-17T01:00:00Z","level":"INFO","msg":"service started"}` + "\n" +
		`{"time":"2026-06-17T01:01:00Z","level":"WARN","msg":"retry save","module":"rdb"}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write log file error = %v", err)
	}
	local, err := agent.New(agent.Config{
		Files: map[string]agent.File{"log": {Path: path, Kind: contracts.KindLog}},
	})
	if err != nil {
		t.Fatalf("agent New error = %v", err)
	}
	reg := rpc.NewRegistry()
	if err := Register(reg, local); err != nil {
		t.Fatalf("Register error = %v", err)
	}
	bus := rpc.NewLocalBus(rpc.LocalConfig{ServiceName: "observe-test", Buffer: 8, DefaultTimeout: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := rpc.Start(ctx, bus, reg, rpc.RunnerOptions{Workers: 1}); err != nil {
		t.Fatalf("rpc Start error = %v", err)
	}
	defer bus.Close(context.Background())

	req, _ := structpb.NewStruct(map[string]any{"file": "log", "contains": "retry"})
	var rsp structpb.Struct
	if err := bus.RequestPB(ctx, 0, TopicTail, req, &rsp, rpc.RequestOptions{Timeout: time.Second}); err != nil {
		t.Fatalf("RequestPB error = %v", err)
	}
	entries := rsp.AsMap()["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	entry := entries[0].(map[string]any)
	if entry["message"] != "retry save" || entry["level"] != "warn" {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestRegisterStatsAndHealthRoutes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.log")
	if err := os.WriteFile(path, []byte("line\n"), 0644); err != nil {
		t.Fatalf("write log file error = %v", err)
	}
	local, err := agent.New(agent.Config{
		Service: "game",
		Files:   map[string]agent.File{"log": {Path: path, Kind: contracts.KindLog}},
	})
	if err != nil {
		t.Fatalf("agent New error = %v", err)
	}
	reg := rpc.NewRegistry()
	if err := Register(reg, local); err != nil {
		t.Fatalf("Register error = %v", err)
	}
	bus := rpc.NewLocalBus(rpc.LocalConfig{ServiceName: "observe-test", Buffer: 8, DefaultTimeout: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := rpc.Start(ctx, bus, reg, rpc.RunnerOptions{Workers: 1}); err != nil {
		t.Fatalf("rpc Start error = %v", err)
	}
	defer bus.Close(context.Background())

	req, _ := structpb.NewStruct(map[string]any{})
	var stats structpb.Struct
	if err := bus.RequestPB(ctx, 0, TopicStats, req, &stats, rpc.RequestOptions{Timeout: time.Second}); err != nil {
		t.Fatalf("stats RequestPB error = %v", err)
	}
	if stats.AsMap()["service"] != "game" {
		t.Fatalf("stats = %+v", stats.AsMap())
	}
	var health structpb.Struct
	if err := bus.RequestPB(ctx, 0, TopicHealth, req, &health, rpc.RequestOptions{Timeout: time.Second}); err != nil {
		t.Fatalf("health RequestPB error = %v", err)
	}
	if health.AsMap()["ok"] != true {
		t.Fatalf("health = %+v", health.AsMap())
	}
}

func TestRegisterTailRouteReturnsConcurrencyError(t *testing.T) {
	reg := rpc.NewRegistry()
	if err := Register(reg, fakeLocalAgent{tailErr: agent.ErrQueryConcurrencyLimit}); err != nil {
		t.Fatalf("Register error = %v", err)
	}
	bus := rpc.NewLocalBus(rpc.LocalConfig{ServiceName: "observe-test", Buffer: 8, DefaultTimeout: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := rpc.Start(ctx, bus, reg, rpc.RunnerOptions{Workers: 1}); err != nil {
		t.Fatalf("rpc Start error = %v", err)
	}
	defer bus.Close(context.Background())

	req, _ := structpb.NewStruct(map[string]any{"file": "log"})
	msg, err := rpccontracts.NewMessagePB(TopicTail, req)
	if err != nil {
		t.Fatalf("NewMessagePB error = %v", err)
	}
	rsp, err := bus.Request(ctx, 0, msg, rpc.RequestOptions{Timeout: time.Second})
	if err != nil {
		t.Fatalf("Request error = %v", err)
	}
	if rsp.Header[rpccontracts.HeaderStatus] != rpc.CodeInvalid {
		t.Fatalf("status = %q, want %s", rsp.Header[rpccontracts.HeaderStatus], rpc.CodeInvalid)
	}
	if !strings.Contains(rsp.Header[rpccontracts.HeaderErrorMessage], agent.ErrQueryConcurrencyLimit.Error()) {
		t.Fatalf("error message = %q", rsp.Header[rpccontracts.HeaderErrorMessage])
	}
}

type fakeLocalAgent struct {
	tailErr error
}

func (f fakeLocalAgent) Tail(context.Context, agent.TailQuery) (agent.TailResult, error) {
	return agent.TailResult{}, f.tailErr
}

func (f fakeLocalAgent) Stats() agent.Stats {
	return agent.Stats{}
}

func (f fakeLocalAgent) Health() agent.Health {
	return agent.Health{OK: true}
}

func (f fakeLocalAgent) Flush(context.Context) error {
	return nil
}
