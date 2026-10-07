package runtime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-observe/contracts"
)

// ErrAsyncSinkClosed 在 async sink 关闭后继续 emit 时返回。
var ErrAsyncSinkClosed = errors.New("observe async sink closed")

// AsyncSink 在本地缓冲事件，并批量写入底层 Sink；Emit 不等待底层 Sink。
type AsyncSink struct {
	sink          contracts.Sink
	queue         chan contracts.Event
	batchSize     int
	flushInterval time.Duration
	dropped       atomic.Uint64
	closed        atomic.Bool
	wg            sync.WaitGroup
	closeOnce     sync.Once
}

// AsyncOptions 控制本地缓冲行为。
type AsyncOptions struct {
	QueueSize     int
	BatchSize     int
	FlushInterval time.Duration
}

// NewAsyncSink 创建带有默认有界缓冲的 async sink。
func NewAsyncSink(sink contracts.Sink, opts AsyncOptions) *AsyncSink {
	if opts.QueueSize <= 0 {
		opts.QueueSize = 1024
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 64
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = time.Second
	}
	return &AsyncSink{
		sink:          sink,
		queue:         make(chan contracts.Event, opts.QueueSize),
		batchSize:     opts.BatchSize,
		flushInterval: opts.FlushInterval,
	}
}

// Start 启动后台 flush worker。
func (s *AsyncSink) Start(ctx context.Context) {
	s.wg.Add(1)
	go s.loop(ctx)
}

// Emit 将事件放入队列；如果本地缓冲已满则丢弃。
func (s *AsyncSink) Emit(event contracts.Event) error {
	if s.closed.Load() {
		return ErrAsyncSinkClosed
	}
	select {
	case s.queue <- event.Clone():
		return nil
	default:
		s.dropped.Add(1)
		return nil
	}
}

// Dropped 返回因队列已满而跳过的事件数量。
func (s *AsyncSink) Dropped() uint64 {
	return s.dropped.Load()
}

// Close 排空已入队事件，并等待 worker 停止。
func (s *AsyncSink) Close() {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		close(s.queue)
		s.wg.Wait()
	})
}

func (s *AsyncSink) loop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()
	batch := make([]contracts.Event, 0, s.batchSize)
	flush := func() {
		if len(batch) == 0 || s.sink == nil {
			batch = batch[:0]
			return
		}
		_ = s.sink.Write(ctx, batch)
		batch = batch[:0]
	}
	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case event, ok := <-s.queue:
			if !ok {
				flush()
				return
			}
			batch = append(batch, event)
			if len(batch) >= s.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}
