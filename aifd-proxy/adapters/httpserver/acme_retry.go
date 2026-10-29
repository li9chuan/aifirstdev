package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/certmagic"
)

const retryStateKey = "aifd/acme-retry.json"

type retryEntry struct {
	NotBefore time.Time `json:"not_before"`
	Failures  int       `json:"failures"`
}

// acmeRetry keeps acknowledged cooldowns in the same private, atomic storage as
// certificate assets. Storage failures stop further requests until recovery.
type acmeRetry struct {
	mu      sync.Mutex
	store   certmagic.Storage
	entries map[string]retryEntry
	err     error
	now     func() time.Time
}

func newACMERetry(ctx context.Context, store certmagic.Storage) (*acmeRetry, error) {
	r := &acmeRetry{store: store, entries: make(map[string]retryEntry), now: time.Now}
	data, err := store.Load(ctx, retryStateKey)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("http acme: read cooldown state: %w", err)
	}
	var saved struct {
		Version int                   `json:"version"`
		Entries map[string]retryEntry `json:"entries"`
	}
	if json.Unmarshal(data, &saved) != nil || saved.Version != 1 || saved.Entries == nil {
		return nil, fmt.Errorf("http acme: invalid cooldown state")
	}
	for key, entry := range saved.Entries {
		if key == "" || entry.NotBefore.IsZero() || entry.Failures < 1 || entry.Failures > 16 {
			return nil, fmt.Errorf("http acme: invalid cooldown entry")
		}
	}
	r.entries = saved.Entries
	return r, nil
}

// wait rechecks after each sleep, so another response can extend a deadline.
func (r *acmeRetry) wait(ctx context.Context, key string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.mu.Lock()
		entry, fatal := r.entries[key], r.err
		remaining := entry.NotBefore.Sub(r.now())
		r.mu.Unlock()
		if fatal != nil {
			return fatal
		}
		if remaining <= 0 {
			return nil
		}
		timer := time.NewTimer(remaining)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

// failed never shortens an existing deadline. The fallback backoff also covers
// network/validation failures and missing or malformed Retry-After headers.
func (r *acmeRetry) failed(key, retryAfter string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.entries[key]
	if entry.Failures < 16 {
		entry.Failures++
	}
	delay := min(30*time.Second*time.Duration(1<<uint(entry.Failures-1)), 6*time.Hour)
	deadline := r.now().Add(delay)
	if server, ok := retryAfterDeadline(retryAfter, r.now()); ok && server.After(deadline) {
		deadline = server
	}
	if deadline.After(entry.NotBefore) {
		entry.NotBefore = deadline
	}
	r.entries[key] = entry
	return r.persistLocked()
}

func (r *acmeRetry) succeeded(key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry, ok := r.entries[key]; !ok || entry.NotBefore.After(r.now()) {
		return r.err
	}
	delete(r.entries, key)
	return r.persistLocked()
}

func (r *acmeRetry) persistLocked() error {
	data, err := json.Marshal(struct {
		Version int                   `json:"version"`
		Entries map[string]retryEntry `json:"entries"`
	}{1, r.entries})
	if err == nil {
		// Request cancellation must not discard a cooldown already received.
		err = r.store.Store(context.Background(), retryStateKey, data)
	}
	if err != nil {
		r.err = fmt.Errorf("http acme: persist cooldown state: %w", err)
	}
	return r.err
}

func retryAfterDeadline(value string, now time.Time) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseUint(value, 10, 64); err == nil {
		const maxSeconds = uint64((1<<63 - 1) / int64(time.Second))
		return now.Add(time.Duration(min(seconds, maxSeconds)) * time.Second), true
	}
	deadline, err := http.ParseTime(value)
	return deadline, err == nil
}

type acmeRetryTransport struct {
	base  http.RoundTripper
	retry *acmeRetry
	event func(string)
}

// RoundTrip gates every CA request, including retries inside the ACME library.
// A CA-wide pause is deliberately conservative: response headers do not identify
// which account/domain/IP quota was exhausted. In-flight requests may finish.
func (t *acmeRetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	key := "ca/" + strings.ToLower(req.URL.Scheme+"://"+req.URL.Host)
	if err := t.retry.wait(req.Context(), key); err != nil {
		return nil, err
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		if req.Context().Err() == nil {
			err = errors.Join(err, t.retry.failed(key, ""))
		}
		return nil, err
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		if err := t.retry.failed(key, resp.Header.Get("Retry-After")); err != nil {
			resp.Body.Close()
			return nil, err
		}
		if t.event != nil {
			t.event("ca_cooldown")
		}
	} else if resp.StatusCode < 400 {
		if err := t.retry.succeeded(key); err != nil {
			resp.Body.Close()
			return nil, err
		}
	}
	return resp, nil
}

func (t *acmeRetryTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
