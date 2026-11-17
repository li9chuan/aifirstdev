package phoneotp

import (
	"context"
	"crypto/subtle"
	"sync"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-account/credential"
)

type memoryChallenge struct {
	PendingChallenge
	active bool
}

// MemoryStore is a bounded single-process reference store. Restart loses
// challenges and cooldowns; it is not a distributed rate limiter.
type MemoryStore struct {
	mu      sync.Mutex
	records map[string]memoryChallenge
	rates   map[string]time.Time
	limit   int
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{records: make(map[string]memoryChallenge), rates: make(map[string]time.Time), limit: 10000}
}

func (s *MemoryStore) Create(ctx context.Context, r PendingChallenge, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, v := range s.records {
		if !now.Before(v.ExpiresAt) {
			delete(s.records, id)
		}
	}
	for key, until := range s.rates {
		if !now.Before(until) {
			delete(s.rates, key)
		}
	}
	if now.Before(s.rates[r.RateKey]) {
		return ErrRateLimited
	}
	if len(s.records) >= s.limit || len(s.rates) >= s.limit {
		return credential.ErrUnavailable
	}
	if _, ok := s.records[r.ID]; ok {
		return credential.ErrUnavailable
	}
	s.rates[r.RateKey] = r.RetryAt
	s.records[r.ID] = memoryChallenge{PendingChallenge: r}
	return nil
}
func (s *MemoryStore) Activate(ctx context.Context, id string, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok || !now.Before(r.ExpiresAt) {
		return credential.ErrInvalidCredential
	}
	r.active = true
	s.records[id] = r
	return nil
}
func (s *MemoryStore) Cancel(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, id)
	return nil
}
func (s *MemoryStore) Consume(ctx context.Context, id string, binding credential.ChallengeBinding, digest [32]byte, now time.Time) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok || !now.Before(r.ExpiresAt) || !r.active || r.Binding != binding {
		return "", credential.ErrInvalidCredential
	}
	if subtle.ConstantTimeCompare(r.CodeDigest[:], digest[:]) != 1 {
		r.Attempts--
		if r.Attempts <= 0 {
			delete(s.records, id)
		} else {
			s.records[id] = r
		}
		return "", credential.ErrInvalidCredential
	}
	delete(s.records, id)
	return r.Phone, nil
}
