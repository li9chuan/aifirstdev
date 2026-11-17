package wechat

import (
	"context"
	"sync"
	"time"

	"github.com/li9chuan/aifirstdev/aifd-account/credential"
)

// MemoryWebsiteStore is a bounded single-process reference. Expired or lost
// challenges require restarting authorization, never accepting a bare callback.
type MemoryWebsiteStore struct {
	mu      sync.Mutex
	records map[string]AuthorizationRecord
}

func NewMemoryWebsiteStore() *MemoryWebsiteStore {
	return &MemoryWebsiteStore{records: make(map[string]AuthorizationRecord)}
}
func (s *MemoryWebsiteStore) Create(ctx context.Context, r AuthorizationRecord, now time.Time) error {
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
	if len(s.records) >= 10000 {
		return credential.ErrUnavailable
	}
	if _, ok := s.records[r.ID]; ok {
		return credential.ErrUnavailable
	}
	s.records[r.ID] = r
	return nil
}
func (s *MemoryWebsiteStore) Consume(ctx context.Context, id string, digest [32]byte, binding credential.ChallengeBinding, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok || !now.Before(r.ExpiresAt) || r.StateDigest != digest || r.Binding != binding {
		return credential.ErrInvalidCredential
	}
	delete(s.records, id)
	return nil
}
