package websocket

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
)

func validateAuthConfig(cfg Config) error {
	switch cfg.AuthCheckMode {
	case proxy.AuthCheckExpiryOnly:
		if cfg.AuthRevalidator != nil || cfg.AuthRecheckInterval != 0 || cfg.AuthCheckTimeout != 0 {
			return fmt.Errorf("%w: expiry-only must not configure revalidation", ErrConfigMissing)
		}
	case proxy.AuthCheckPeriodic, proxy.AuthCheckPerRequest:
		if cfg.AuthRevalidator == nil || cfg.AuthRecheckInterval <= 0 || cfg.AuthCheckTimeout <= 0 {
			return fmt.Errorf("%w: revalidation requires a checker and positive durations", ErrConfigMissing)
		}
	default:
		return fmt.Errorf("%w: select an explicit authentication check mode", ErrConfigMissing)
	}
	return nil
}

// connectionAuth owns one immutable authentication deadline and the local
// knowledge of its invalidation. No network call runs under a state mutex.
type connectionAuth struct {
	snapshot            proxy.AuthContext
	mode                proxy.AuthCheckMode
	revalidator         proxy.AuthRevalidator
	interval, timeLimit time.Duration
	nextCheck           atomic.Int64
	invalid             atomic.Bool
	once                sync.Once
	terminate           func()
}

func newConnectionAuth(cfg Config, snapshot proxy.AuthContext, terminate func()) *connectionAuth {
	a := &connectionAuth{snapshot: snapshot, mode: cfg.AuthCheckMode, revalidator: cfg.AuthRevalidator, interval: cfg.AuthRecheckInterval, timeLimit: cfg.AuthCheckTimeout, terminate: terminate}
	a.nextCheck.Store(time.Now().Add(a.interval).UnixNano())
	return a
}

func (a *connectionAuth) checkLocal() error {
	if a.invalid.Load() {
		return proxy.ErrAuthContextInvalid
	}
	if !time.Now().Before(a.snapshot.ValidUntil) {
		return proxy.ErrAuthExpired
	}
	return nil
}

// check never shares a previous success with a PerRequest caller. Periodic
// callers past the window query authority even if the timer has been delayed.
func (a *connectionAuth) check(ctx context.Context, inbound bool) error {
	if err := a.checkLocal(); err != nil {
		a.fail()
		return err
	}
	if a.mode == proxy.AuthCheckExpiryOnly {
		return nil
	}
	if !(inbound && a.mode == proxy.AuthCheckPerRequest) && time.Now().UnixNano() < a.nextCheck.Load() {
		return nil
	}
	deadline := time.Now().Add(a.timeLimit)
	if a.snapshot.ValidUntil.Before(deadline) {
		deadline = a.snapshot.ValidUntil
	}
	checkCtx, cancel := context.WithDeadline(ctx, deadline)
	// Invalidate locally at timeout even if the checker returns late. Checkers
	// must honor context cancellation to release their upstream work.
	stopTimeout := context.AfterFunc(checkCtx, a.fail)
	err := a.revalidator.Revalidate(checkCtx, a.snapshot)
	if err == nil {
		err = checkCtx.Err()
	}
	stopTimeout()
	cancel()
	if err != nil {
		a.fail()
		return err
	}
	if err = a.checkLocal(); err != nil {
		a.fail()
		return err
	}
	next := time.Now().Add(a.interval).UnixNano()
	for old := a.nextCheck.Load(); next > old; old = a.nextCheck.Load() {
		if a.nextCheck.CompareAndSwap(old, next) {
			break
		}
	}
	return nil
}

func (a *connectionAuth) fail() {
	a.invalid.Store(true)
	a.once.Do(func() { go a.terminate() })
}

// monitor handles idle and push-only connections. Send independently checks the
// deadline after acquiring the actual write lock, regardless of timer latency.
func (a *connectionAuth) monitor(ctx context.Context) {
	for {
		next := a.snapshot.ValidUntil
		if a.mode != proxy.AuthCheckExpiryOnly {
			check := time.Unix(0, a.nextCheck.Load())
			if check.Before(next) {
				next = check
			}
		}
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			if err := a.check(ctx, false); err != nil {
				return
			}
		}
	}
}
