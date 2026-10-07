package runtime

import (
	"context"
	goruntime "runtime"
	"sync"
	"sync/atomic"

	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
)

// Runner 消费 delivery，并按需使用 UID 分片执行 handler。
type Runner struct {
	deliveries <-chan rpc.Delivery
	workers    int
	queueSize  int
	roundRobin atomic.Uint64
	wg         sync.WaitGroup
}

// RunnerOptions 配置 worker 数量和单 worker 队列深度。
type RunnerOptions struct {
	Workers   int
	QueueSize int
}

// NewRunner 创建 delivery runner；未传配置时使用默认配置。
func NewRunner(deliveries <-chan rpc.Delivery, options ...RunnerOptions) *Runner {
	var opts RunnerOptions
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.Workers <= 0 {
		opts.Workers = goruntime.NumCPU()*2 + 2
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = 1024
	}
	return &Runner{deliveries: deliveries, workers: opts.Workers, queueSize: opts.QueueSize}
}

// Run 使用默认 runner 消费 delivery，适合服务启动层直接挂后台 goroutine。
func Run(ctx context.Context, deliveries <-chan rpc.Delivery, options ...RunnerOptions) {
	NewRunner(deliveries, options...).Run(ctx)
}

// Run 将 delivery 分发给 worker，直到输入 channel 结束。
func (r *Runner) Run(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	workerChans := make([]chan rpc.Delivery, r.workers)
	for i := range workerChans {
		workerChans[i] = make(chan rpc.Delivery, r.queueSize)
		r.wg.Add(1)
		go r.worker(ctx, workerChans[i])
	}
	defer func() {
		for _, ch := range workerChans {
			close(ch)
		}
		r.wg.Wait()
	}()

	for delivery := range r.deliveries {
		idx := r.workerIndex(delivery)
		workerChans[idx] <- delivery
	}
}

func (r *Runner) worker(ctx context.Context, deliveries <-chan rpc.Delivery) {
	defer r.wg.Done()
	for delivery := range deliveries {
		_ = delivery.Handle(ctx)
	}
}

func (r *Runner) workerIndex(delivery rpc.Delivery) int {
	if r.workers <= 1 || delivery == nil || delivery.Session() == nil {
		return 0
	}
	uid := delivery.Session().UID()
	if uid > 0 {
		return int(uint64(uid) % uint64(r.workers))
	}
	return int(r.roundRobin.Add(1) % uint64(r.workers))
}

// HashProcess 保留当前项目的 hash 规则，供需要在 Runner 外显式选择队列的调用方使用。
func HashProcess(uid int64, maxProc uint32, seq *uint32) uint32 {
	if maxProc == 0 {
		return 0
	}
	if uid > 0 {
		return uint32(uid) % maxProc
	}
	if seq == nil {
		return 0
	}
	*seq = *seq + 1
	return *seq % maxProc
}
