package httpserver

import (
	"context"
	"errors"
	"sync"

	"github.com/caddyserver/certmagic"
)

// managedStorage tracks complete locked certificate jobs, including their
// deferred writes and unlocks. Cache.Stop alone does not join CertMagic's
// process-wide asynchronous job manager.
type managedStorage struct {
	certmagic.Storage
	mu      sync.Mutex
	closing bool
	held    map[string]int
	jobs    sync.WaitGroup
	err     error
}

func (s *managedStorage) Lock(ctx context.Context, key string) error {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return context.Canceled
	}
	s.jobs.Add(1)
	s.mu.Unlock()
	if err := s.Storage.Lock(ctx, key); err != nil {
		s.jobs.Done()
		return err
	}
	s.mu.Lock()
	if s.held == nil {
		s.held = make(map[string]int)
	}
	s.held[key]++
	s.mu.Unlock()
	return nil
}

func (s *managedStorage) Unlock(ctx context.Context, key string) error {
	err := s.Storage.Unlock(ctx, key)
	s.mu.Lock()
	if err != nil {
		s.err = errors.Join(s.err, err)
	}
	if s.held[key] > 0 {
		s.held[key]--
		s.jobs.Done()
	}
	s.mu.Unlock()
	return err
}

func (s *managedStorage) closeError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *managedStorage) stopNewJobs() {
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
}
