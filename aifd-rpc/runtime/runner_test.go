package runtime

import (
	"context"
	goruntime "runtime"
	"testing"
	"time"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

func TestNewRunnerUsesAutoWorkersByDefault(t *testing.T) {
	runner := NewRunner(nil)
	wantWorkers := goruntime.NumCPU()*2 + 2
	if runner.workers != wantWorkers {
		t.Fatalf("workers = %d, want %d", runner.workers, wantWorkers)
	}
	if runner.queueSize != 1024 {
		t.Fatalf("queueSize = %d, want 1024", runner.queueSize)
	}
}

func TestNewRunnerKeepsExplicitWorkerCount(t *testing.T) {
	runner := NewRunner(nil, RunnerOptions{Workers: 1, QueueSize: 8})
	if runner.workers != 1 {
		t.Fatalf("workers = %d, want 1", runner.workers)
	}
	if runner.queueSize != 8 {
		t.Fatalf("queueSize = %d, want 8", runner.queueSize)
	}
}

func TestRunHelperConsumesDeliveries(t *testing.T) {
	deliveries := make(chan rpc.Delivery, 1)
	handled := make(chan struct{}, 1)
	deliveries <- NewDelivery(rpc.Route{
		Name: "RunHelper",
		Handler: func(context.Context, rpc.Session, rpc.Message) error {
			handled <- struct{}{}
			return nil
		},
	}, NewSession(SessionOptions{UID: 1}), rpc.Message{Name: "RunHelper"}, nil)
	close(deliveries)

	Run(context.Background(), deliveries, RunnerOptions{Workers: 1, QueueSize: 1})

	select {
	case <-handled:
	default:
		t.Fatal("Run helper did not handle delivery")
	}
}

func TestRunHelperDrainsDeliveriesAfterContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	deliveries := make(chan rpc.Delivery, 1)
	handled := make(chan struct{}, 1)
	deliveries <- NewDelivery(rpc.Route{
		Name: "RunDrain",
		Handler: func(context.Context, rpc.Session, rpc.Message) error {
			handled <- struct{}{}
			return nil
		},
	}, NewSession(SessionOptions{UID: 1}), rpc.Message{Name: "RunDrain"}, nil)
	close(deliveries)

	Run(ctx, deliveries, RunnerOptions{Workers: 1, QueueSize: 1})

	select {
	case <-handled:
	default:
		t.Fatal("Run helper did not drain delivery after context cancel")
	}
}

func TestDeliveryHandleRecordsLatencyAndRecovers(t *testing.T) {
	stats := NewStatsCollector("test", time.Minute)
	delivery := NewDelivery(rpc.Route{
		Name: "PanicRoute",
		Handler: func(context.Context, rpc.Session, rpc.Message) error {
			panic("boom")
		},
	}, NewSession(SessionOptions{UID: 7}), rpc.Message{Name: "PanicRoute"}, stats)

	if err := delivery.Handle(context.Background()); err == nil {
		t.Fatal("Handle error = nil, want panic error")
	}
	stat := stats.Snapshot().Stats[0]
	if stat.Total.Handle.Count != 1 {
		t.Fatalf("handle count = %d, want 1", stat.Total.Handle.Count)
	}
}

func TestRunnerWorkerIndexUsesUIDAndRoundRobin(t *testing.T) {
	runner := NewRunner(nil, RunnerOptions{Workers: 4})
	d1 := NewDelivery(rpc.Route{Name: "A", Handler: func(context.Context, rpc.Session, rpc.Message) error { return nil }}, NewSession(SessionOptions{UID: 9}), rpc.Message{Name: "A"}, nil)
	d2 := NewDelivery(rpc.Route{Name: "A", Handler: func(context.Context, rpc.Session, rpc.Message) error { return nil }}, NewSession(SessionOptions{UID: 9}), rpc.Message{Name: "A"}, nil)
	if runner.workerIndex(d1) != runner.workerIndex(d2) {
		t.Fatal("same uid should use same worker")
	}

	z1 := NewDelivery(rpc.Route{Name: "A", Handler: func(context.Context, rpc.Session, rpc.Message) error { return nil }}, NewSession(SessionOptions{}), rpc.Message{Name: "A"}, nil)
	z2 := NewDelivery(rpc.Route{Name: "A", Handler: func(context.Context, rpc.Session, rpc.Message) error { return nil }}, NewSession(SessionOptions{}), rpc.Message{Name: "A"}, nil)
	if runner.workerIndex(z1) == runner.workerIndex(z2) {
		t.Fatal("zero uid should round robin")
	}
}
