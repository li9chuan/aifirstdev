package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/li9chuan/aifirstdev/aifd-observe/logging"
	proxy "github.com/li9chuan/aifirstdev/aifd-proxy/contracts"
	proxypb "github.com/li9chuan/aifirstdev/aifd-schema/generated/proxy"
)

var (
	ErrSessionNotBound = errors.New("client session is not bound")
	ErrSessionExpired  = errors.New("client session has expired")
	ErrSessionClosed   = errors.New("client session is closed")
)

type sessionKey struct{ instance, connection string }
type clientLease struct {
	uid      int64
	deadline time.Time
}

// ClientSessionOptions controls ephemeral, per-connection liveness. Hooks run
// without the lease lock and must not assume a loaded business User.
type ClientSessionOptions struct {
	TTL      time.Duration
	Now      func() time.Time
	OnOpened func(context.Context, int64, *proxypb.ClientSessionRef) error
	OnClosed func(context.Context, int64, *proxypb.ClientSessionRef, proxypb.SessionEndReason) error
}

// ClientSessions owns leases, not business users or sockets. Explicit Open is the
// only operation that creates a lease. Renew cannot resurrect a closed connection.
type ClientSessions struct {
	mu      sync.Mutex
	leases  map[sessionKey]clientLease
	retired map[sessionKey]time.Time
	opts    ClientSessionOptions
	closed  bool
	running atomic.Bool
}

// NewClientSessions creates a component with no implicit background goroutine.
func NewClientSessions(opts ClientSessionOptions) *ClientSessions {
	if opts.TTL <= 0 {
		opts.TTL = 2 * time.Minute
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &ClientSessions{leases: make(map[sessionKey]clientLease), retired: make(map[sessionKey]time.Time), opts: opts}
}

// Open accepts only a previously authenticated live proxy reference. Repeated Open
// is idempotent and does not renew; a different UID cannot steal the same reference.
func (m *ClientSessions) Open(ctx context.Context, uid int64, ref *proxypb.ClientSessionRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if uid <= 0 || !proxy.ValidSessionRef(ref) {
		return proxy.ErrClientSessionMissing
	}
	key, now := keyOf(ref), m.opts.Now()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrSessionClosed
	}
	if until, ok := m.retired[key]; ok && now.Before(until) {
		m.mu.Unlock()
		return ErrSessionClosed
	}
	if current, ok := m.leases[key]; ok {
		m.mu.Unlock()
		if current.uid != uid {
			return ErrInvalidIdentity
		}
		if !now.Before(current.deadline) {
			_ = m.End(ctx, uid, ref, proxypb.SessionEndReason_SESSION_END_REASON_EXPIRED)
			return ErrSessionExpired
		}
		return nil
	}
	m.leases[key] = clientLease{uid: uid, deadline: now.Add(m.opts.TTL)}
	m.mu.Unlock()
	if m.opts.OnOpened != nil {
		if err := callSessionHook(func() error { return m.opts.OnOpened(ctx, uid, refOf(key)) }); err != nil {
			_ = m.End(ctx, uid, ref, proxypb.SessionEndReason_SESSION_END_REASON_DISCONNECTED)
			return err
		}
	}
	return m.Validate(ctx, uid, ref)
}

// Validate checks liveness without extending the deadline.
func (m *ClientSessions) Validate(ctx context.Context, uid int64, ref *proxypb.ClientSessionRef) error {
	return m.check(ctx, uid, ref, false)
}

// Renew extends only the exact, already-live UID/reference binding.
func (m *ClientSessions) Renew(ctx context.Context, uid int64, ref *proxypb.ClientSessionRef) error {
	return m.check(ctx, uid, ref, true)
}

func (m *ClientSessions) check(ctx context.Context, uid int64, ref *proxypb.ClientSessionRef, renew bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if uid <= 0 || !proxy.ValidSessionRef(ref) {
		return proxy.ErrClientSessionMissing
	}
	key, now := keyOf(ref), m.opts.Now()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrSessionClosed
	}
	current, ok := m.leases[key]
	if !ok {
		m.mu.Unlock()
		return ErrSessionNotBound
	}
	if current.uid != uid {
		m.mu.Unlock()
		return ErrInvalidIdentity
	}
	if !now.Before(current.deadline) {
		delete(m.leases, key)
		m.retired[key] = now.Add(m.retirementWindow())
		m.mu.Unlock()
		_ = m.notifyClosed(ctx, uid, key, proxypb.SessionEndReason_SESSION_END_REASON_EXPIRED)
		return ErrSessionExpired
	}
	if renew {
		current.deadline = now.Add(m.opts.TTL)
		m.leases[key] = current
	}
	m.mu.Unlock()
	return nil
}

// End is idempotent and closes only one full connection reference. A close that
// arrives before Open retires the reference as well, preventing reordered activation.
func (m *ClientSessions) End(ctx context.Context, uid int64, ref *proxypb.ClientSessionRef, reason proxypb.SessionEndReason) error {
	if uid <= 0 || !proxy.ValidSessionRef(ref) {
		return proxy.ErrClientSessionMissing
	}
	key := keyOf(ref)
	m.mu.Lock()
	current, ok := m.leases[key]
	if ok && current.uid != uid {
		m.mu.Unlock()
		return ErrInvalidIdentity
	}
	delete(m.leases, key)
	m.retired[key] = m.opts.Now().Add(m.retirementWindow())
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return m.notifyClosed(ctx, uid, key, reason)
}

// Count reports currently live connections without loading a User or renewing.
func (m *ClientSessions) Count(uid int64) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0
	}
	now, count := m.opts.Now(), 0
	for _, current := range m.leases {
		if current.uid == uid && now.Before(current.deadline) {
			count++
		}
	}
	return count
}

// Expire removes elapsed leases and old retirement markers. Retired references are
// retained at least as long as a bounded lifecycle RPC can remain in flight.
// Heartbeats always reject unknown references, including after marker collection.
func (m *ClientSessions) Expire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now := m.opts.Now()
	m.mu.Lock()
	expired := make(map[sessionKey]clientLease)
	for key, current := range m.leases {
		if !now.Before(current.deadline) {
			expired[key] = current
			delete(m.leases, key)
			m.retired[key] = now.Add(m.retirementWindow())
		}
	}
	for key, until := range m.retired {
		if !now.Before(until) {
			delete(m.retired, key)
		}
	}
	m.mu.Unlock()
	var joined error
	for key, current := range expired {
		joined = errors.Join(joined, m.notifyClosed(ctx, current.uid, key, proxypb.SessionEndReason_SESSION_END_REASON_EXPIRED))
	}
	return joined
}

// Run owns the expiry loop; the application invokes it in its service lifecycle.
func (m *ClientSessions) Run(ctx context.Context) error {
	if !m.running.CompareAndSwap(false, true) {
		return fmt.Errorf("client session expiry loop already running")
	}
	defer m.running.Store(false)
	interval := min(m.opts.TTL/4, time.Second)
	if interval <= 0 {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := m.Expire(ctx); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				log.Warnf("client lease expiry hook: %v", err)
			}
		}
	}
}

// Close retires all live references and rejects further activation.
func (m *ClientSessions) Close(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	current := m.leases
	m.leases = make(map[sessionKey]clientLease)
	m.retired = make(map[sessionKey]time.Time)
	m.mu.Unlock()
	var joined error
	for key, lease := range current {
		joined = errors.Join(joined, m.notifyClosed(ctx, lease.uid, key, proxypb.SessionEndReason_SESSION_END_REASON_SHUTDOWN))
	}
	return joined
}

func (m *ClientSessions) notifyClosed(ctx context.Context, uid int64, key sessionKey, reason proxypb.SessionEndReason) error {
	if m.opts.OnClosed == nil {
		return nil
	}
	return callSessionHook(func() error { return m.opts.OnClosed(ctx, uid, refOf(key), reason) })
}

func (m *ClientSessions) retirementWindow() time.Duration {
	return max(m.opts.TTL, sessionRPCMaxTimeout)
}

func callSessionHook(fn func() error) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("client session hook panic: %v", value)
		}
	}()
	return fn()
}

func keyOf(ref *proxypb.ClientSessionRef) sessionKey {
	return sessionKey{ref.GetProxyInstanceId(), ref.GetConnectionId()}
}
func refOf(key sessionKey) *proxypb.ClientSessionRef {
	return &proxypb.ClientSessionRef{ProxyInstanceId: key.instance, ConnectionId: key.connection}
}
