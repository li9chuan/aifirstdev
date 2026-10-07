package lifecycle

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// TickFunc 推进一次 lifecycle。
type TickFunc func(context.Context, time.Time, string) error

// Runner 拥有低频全局状态推进逻辑。
type Runner struct {
	name      string
	interval  time.Duration
	tick      TickFunc
	recovered atomic.Bool
	mu        sync.Mutex
}

// NewRunner 创建需要显式完成恢复后才允许运行的 lifecycle runner。
func NewRunner(name string, interval time.Duration, tick TickFunc) *Runner {
	return &Runner{name: name, interval: interval, tick: tick}
}

// MarkRecovered 打开允许 tick 运行的恢复门禁。
func (r *Runner) MarkRecovered() {
	r.recovered.Store(true)
}

// Recovered 返回当前 runner 是否已完成恢复。
func (r *Runner) Recovered() bool {
	return r.recovered.Load()
}

// RunOnce 在恢复完成且当前没有 tick 运行时执行一次 tick。
func (r *Runner) RunOnce(ctx context.Context, now time.Time, source string) error {
	if !r.recovered.Load() {
		return fmt.Errorf("lifecycle %s recovery not ready", r.name)
	}
	if r.tick == nil {
		return nil
	}
	if !r.mu.TryLock() {
		return nil
	}
	defer r.mu.Unlock()
	return r.tick(ctx, now, source)
}

// Start 在配置了间隔和 tick 函数时启动 ticker 循环。
func (r *Runner) Start(ctx context.Context) {
	if r.interval <= 0 || r.tick == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				_ = r.RunOnce(ctx, now, "ticker")
			}
		}
	}()
}
