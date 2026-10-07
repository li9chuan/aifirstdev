package runtime

import (
	"context"
	"testing"

	"github.com/li9chuan/aifirstdev/aifd-observe/adapters/memory"
	"github.com/li9chuan/aifirstdev/aifd-observe/contracts"
)

func TestKindRouterRoutesByKind(t *testing.T) {
	logs := memory.NewRecorder()
	stats := memory.NewRecorder()
	fallback := memory.NewRecorder()
	router := NewKindRouter(fallback)
	router.Set(contracts.KindLog, logs)
	router.Set(contracts.KindStat, stats)

	err := router.Write(context.Background(), []contracts.Event{
		contracts.Log(contracts.LevelInfo, "log", "started"),
		contracts.Stat("stat-1", "friend.add", "uid:1", 1, nil),
		contracts.Metric("rpc.count", 1, nil),
	})
	if err != nil {
		t.Fatalf("Write error = %v", err)
	}
	if got := len(logs.Events()); got != 1 {
		t.Fatalf("log events = %d, want 1", got)
	}
	if got := len(stats.Events()); got != 1 {
		t.Fatalf("stat events = %d, want 1", got)
	}
	if got := len(fallback.Events()); got != 1 {
		t.Fatalf("fallback events = %d, want 1", got)
	}
}

func TestKindRouterNilSinkDropsKind(t *testing.T) {
	fallback := memory.NewRecorder()
	router := NewKindRouter(fallback)
	router.Set(contracts.KindLog, nil)

	if err := router.Write(context.Background(), []contracts.Event{
		contracts.Log(contracts.LevelInfo, "log", "started"),
		contracts.Metric("rpc.count", 1, nil),
	}); err != nil {
		t.Fatalf("Write error = %v", err)
	}
	if got := len(fallback.Events()); got != 1 {
		t.Fatalf("fallback events = %d, want 1", got)
	}
}
