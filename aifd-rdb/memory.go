package rdb

import (
	"context"
	"sort"
	"sync"
	"time"
)

type record struct {
	data      []byte
	expiresAt time.Time
}

type hashRecord struct {
	fields    map[string][]byte
	expiresAt time.Time
}

type memoryStore struct {
	mu     sync.Mutex
	kv     map[string]record
	hashes map[string]hashRecord
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		kv:     make(map[string]record),
		hashes: make(map[string]hashRecord),
	}
}

func (s *memoryStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.kv[key]
	if !ok || expired(rec.expiresAt) {
		delete(s.kv, key)
		return nil, false, nil
	}
	return cloneBytes(rec.data), true, nil
}

func (s *memoryStore) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	expiresAt := expiresAt(ttl)
	if ttl < 0 {
		if rec, ok := s.kv[key]; ok && !expired(rec.expiresAt) {
			expiresAt = rec.expiresAt
		}
	}
	s.kv[key] = record{data: cloneBytes(value), expiresAt: expiresAt}
	return nil
}

func (s *memoryStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.kv, key)
	return nil
}

func (s *memoryStore) Expire(_ context.Context, key string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ttl <= 0 {
		return nil
	}
	expiresAt := expiresAt(ttl)
	if rec, ok := s.kv[key]; ok {
		if expired(rec.expiresAt) {
			delete(s.kv, key)
		} else {
			rec.expiresAt = expiresAt
			s.kv[key] = rec
		}
	}
	if hash, ok := s.hashes[key]; ok {
		if expired(hash.expiresAt) {
			delete(s.hashes, key)
		} else {
			hash.expiresAt = expiresAt
			s.hashes[key] = hash
		}
	}
	return nil
}

func (s *memoryStore) HGet(_ context.Context, key, field string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hash, ok := s.hashes[key]
	if !ok {
		return nil, false, nil
	}
	if expired(hash.expiresAt) {
		delete(s.hashes, key)
		return nil, false, nil
	}
	value, ok := hash.fields[field]
	if !ok {
		return nil, false, nil
	}
	return cloneBytes(value), true, nil
}

func (s *memoryStore) HSet(_ context.Context, key, field string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	hash, ok := s.hashes[key]
	if !ok || expired(hash.expiresAt) {
		hash = hashRecord{fields: make(map[string][]byte)}
	}
	hash.fields[field] = cloneBytes(value)
	s.hashes[key] = hash
	return nil
}

func (s *memoryStore) HDelete(_ context.Context, key, field string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if hash, ok := s.hashes[key]; ok {
		if expired(hash.expiresAt) {
			delete(s.hashes, key)
			return nil
		}
		delete(hash.fields, field)
		s.hashes[key] = hash
	}
	return nil
}

func (s *memoryStore) HKeys(_ context.Context, key string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hash, ok := s.hashes[key]
	if !ok {
		return nil, nil
	}
	if expired(hash.expiresAt) {
		delete(s.hashes, key)
		return nil, nil
	}
	keys := make([]string, 0, len(hash.fields))
	for field := range hash.fields {
		keys = append(keys, field)
	}
	sort.Strings(keys)
	return keys, nil
}

func cloneBytes(in []byte) []byte {
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func expiresAt(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(ttl)
}

func expired(expiresAt time.Time) bool {
	return !expiresAt.IsZero() && time.Now().After(expiresAt)
}
