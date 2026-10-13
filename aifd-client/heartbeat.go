package client

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

var ErrHeartbeatRunning = errors.New("aifd client heartbeat already running")

// HeartbeatOptions configures end-to-end liveness after authentication. Probe
// constructs and validates the application's typed request/reply and must honor
// its context deadline. The scheduler never treats a proxy-local Pong as success.
type HeartbeatOptions struct {
	Interval  time.Duration
	Timeout   time.Duration
	MaxMisses int
	Probe     func(context.Context) error
	OnLost    func(error)
}

type heartbeatRun struct {
	ctx     context.Context
	cancel  context.CancelFunc
	opts    HeartbeatOptions
	stopped atomic.Bool
}

// StartHeartbeat starts one serialized probe loop. Business calls it only after
// authentication succeeds; an existing loop is an error, never silently duplicated.
func (c *Client) StartHeartbeat(opts HeartbeatOptions) error {
	if opts.Probe == nil {
		return fmt.Errorf("heartbeat probe is nil")
	}
	if opts.Interval <= 0 {
		opts.Interval = 30 * time.Second
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	if opts.MaxMisses <= 0 {
		opts.MaxMisses = 2
	}
	c.heartbeatMu.Lock()
	defer c.heartbeatMu.Unlock()
	if c.closed.Load() {
		return ErrClosed
	}
	if !c.started.Load() {
		return ErrNotStarted
	}
	if c.heartbeat != nil {
		return ErrHeartbeatRunning
	}
	ctx, cancel := context.WithCancel(context.Background())
	current := &heartbeatRun{ctx: ctx, cancel: cancel, opts: opts}
	c.heartbeat = current
	go c.heartbeatLoop(current)
	return nil
}

// StopHeartbeat cancels the loop and current cooperative probe. It is nonblocking,
// so OnLost may safely call Close. It never emits a lost notification itself.
func (c *Client) StopHeartbeat() {
	c.heartbeatMu.Lock()
	current := c.heartbeat
	c.heartbeat = nil
	if current != nil {
		current.stopped.Store(true)
		current.cancel()
	}
	c.heartbeatMu.Unlock()
}

func (c *Client) heartbeatLoop(current *heartbeatRun) {
	var lost error
	defer func() {
		current.cancel()
		c.heartbeatMu.Lock()
		if c.heartbeat == current {
			c.heartbeat = nil
		}
		c.heartbeatMu.Unlock()
		if lost != nil && !current.stopped.Load() && current.opts.OnLost != nil {
			current.opts.OnLost(lost)
		}
	}()
	ticker := time.NewTicker(current.opts.Interval)
	defer ticker.Stop()
	misses := 0
	for {
		select {
		case <-current.ctx.Done():
			return
		case <-c.done:
			if err := c.Err(); !errors.Is(err, ErrClosed) {
				lost = err
			}
			return
		case <-ticker.C:
			probeCtx, cancel := context.WithTimeout(current.ctx, current.opts.Timeout)
			stopTransportCancel := context.AfterFunc(c.ctx, cancel)
			err := runHeartbeatProbe(probeCtx, current.opts.Probe)
			if err == nil {
				err = probeCtx.Err()
			}
			stopTransportCancel()
			cancel()
			if current.ctx.Err() != nil {
				return
			}
			if c.closed.Load() {
				if cause := c.Err(); !errors.Is(cause, ErrClosed) {
					lost = cause
				}
				return
			}
			if err == nil {
				misses = 0
				continue
			}
			misses++
			if misses >= current.opts.MaxMisses {
				lost = fmt.Errorf("heartbeat failed %d times: %w", misses, err)
				return
			}
		}
	}
}

func runHeartbeatProbe(ctx context.Context, probe func(context.Context) error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("heartbeat probe panic: %v", recovered)
		}
	}()
	return probe(ctx)
}
