package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrStatClosed indicates an append attempted after the process Stat file closed.
var ErrStatClosed = errors.New("stat output is closed")

// StatForwardFunc receives one immutable, already encoded CSV record including
// its final newline. Implementations must honor ctx; transport and receiving
// services belong to the application. No automatic retries or replay occur.
type StatForwardFunc func(ctx context.Context, record string) error

// StatConfig enables required local CSV output and optional best-effort forwarding.
// Zero queue/time values use 1024 records, 2 seconds per call and 5 seconds to drain.
type StatConfig struct {
	LocalPath           string
	Rotation            RotationConfig
	Forward             StatForwardFunc
	ForwardQueueSize    int
	ForwardTimeout      time.Duration
	ForwardDrainTimeout time.Duration
}

// statRecorder serializes append/enqueue with close. Only immutable records leave
// this lock; all remote I/O runs in a separate, bounded worker queue.
type statRecorder struct {
	mu       sync.Mutex
	file     *rollingFile
	closed   bool
	queue    chan string
	forward  StatForwardFunc
	timeout  time.Duration
	drain    time.Duration
	cancel   context.CancelFunc
	done     chan struct{}
	once     sync.Once
	closeErr error
	report   func(error)

	localWritten   atomic.Uint64
	forwarded      atomic.Uint64
	forwardErrors  atomic.Uint64
	forwardDropped atomic.Uint64
}

func newStatRecorder(config StatConfig, report func(error)) (*statRecorder, error) {
	var err error
	config, err = config.withDefaults()
	if err != nil {
		return nil, err
	}
	if report == nil {
		report = func(error) {}
	}
	file, err := openLocalFile(config.LocalPath, config.Rotation)
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}
	r := &statRecorder{file: file, forward: config.Forward, timeout: config.ForwardTimeout, drain: config.ForwardDrainTimeout, report: report}
	if config.Forward != nil {
		r.queue = make(chan string, config.ForwardQueueSize)
		r.done = make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		r.cancel = cancel
		go r.run(ctx)
	}
	return r, nil
}

func (config StatConfig) withDefaults() (StatConfig, error) {
	if err := config.Rotation.validate(); err != nil {
		return config, err
	}
	if strings.TrimSpace(config.LocalPath) == "" {
		return config, fmt.Errorf("stat local path is required, including when forwarding")
	}
	if config.ForwardQueueSize < 0 || config.ForwardTimeout < 0 || config.ForwardDrainTimeout < 0 {
		return config, fmt.Errorf("stat forwarding sizes and durations cannot be negative")
	}
	if config.ForwardQueueSize == 0 {
		config.ForwardQueueSize = 1024
	}
	if config.ForwardTimeout == 0 {
		config.ForwardTimeout = 2 * time.Second
	}
	if config.ForwardDrainTimeout == 0 {
		config.ForwardDrainTimeout = 5 * time.Second
	}
	return config, nil
}

func (r *statRecorder) write(record string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrStatClosed
	}
	n, err := r.file.Write([]byte(record))
	if err != nil {
		return fmt.Errorf("stat append local file: %w", err)
	}
	if n != len(record) {
		return io.ErrShortWrite
	}
	r.localWritten.Add(1)
	if r.queue != nil {
		select {
		case r.queue <- record:
		default:
			r.forwardDropped.Add(1)
		}
	}
	return nil
}

func (r *statRecorder) close() error {
	r.once.Do(func() {
		r.mu.Lock()
		r.closed = true
		if r.queue != nil {
			close(r.queue)
		}
		r.closeErr = r.file.Close()
		r.mu.Unlock()
		if r.queue == nil {
			return
		}
		timer := time.NewTimer(r.drain)
		defer timer.Stop()
		defer r.cancel()
		select {
		case <-r.done:
		case <-timer.C:
			r.cancel()
			// Cancel the current call and discard pending remote copies only.
			for range r.queue {
				r.forwardDropped.Add(1)
			}
			err := fmt.Errorf("stat forwarding drain: %w", context.DeadlineExceeded)
			r.report(err)
			r.closeErr = errors.Join(r.closeErr, err)
		}
	})
	return r.closeErr
}

func (r *statRecorder) run(ctx context.Context) {
	defer close(r.done)
	for record := range r.queue {
		if ctx.Err() != nil {
			r.forwardDropped.Add(1)
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, r.timeout)
		err := r.call(callCtx, record)
		cancel()
		if err != nil {
			r.forwardErrors.Add(1)
			r.report(err)
		} else {
			r.forwarded.Add(1)
		}
	}
}

func (r *statRecorder) call(ctx context.Context, record string) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("stat forward panic: %v", recovered)
		}
	}()
	return r.forward(ctx, record)
}
