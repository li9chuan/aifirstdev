package rdb

import (
	"context"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
)

// SetSaver is embedded by business objects that persist one protobuf message under one Redis key.
type SetSaver[T proto.Message] struct {
	mu       sync.RWMutex
	db       *DB
	DbProto  T
	IsDelete bool
	DbKey    string
	ttl      time.Duration
}

// Bind 把 saver 绑定到包级 DefaultDB 的一个 SET key。
func (s *SetSaver[T]) Bind(key string, pb T, opts ...Option) error {
	return s.BindDB(DefaultDB(), key, pb, opts...)
}

// BindDB 把 saver 绑定到显式传入 DB 的一个 SET key。
func (s *SetSaver[T]) BindDB(db *DB, key string, pb T, opts ...Option) error {
	if err := validateSetBinding(db, key, pb); err != nil {
		return err
	}
	cfg, err := applyOptions(opts)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.db = db
	s.DbKey = key
	s.DbProto = pb
	s.ttl = cfg.ttl
	s.IsDelete = false
	return nil
}

// Key returns the Redis key owned by this saver.
func (s *SetSaver[T]) Key() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.DbKey
}

// Load replaces DbProto with the stored protobuf message when the key exists.
func (s *SetSaver[T]) Load(ctx context.Context) (bool, error) {
	s.mu.RLock()
	db, key, template := s.db, s.DbKey, s.DbProto
	s.mu.RUnlock()
	if err := validateSetBinding(db, key, template); err != nil {
		return false, err
	}
	raw, ok, err := db.store.Get(ctx, key)
	if err != nil || !ok {
		return ok, err
	}
	msg, err := newProtoMessage(template)
	if err != nil {
		return false, err
	}
	if err := proto.Unmarshal(raw, msg); err != nil {
		return false, err
	}
	s.mu.Lock()
	s.DbProto = msg
	s.IsDelete = false
	s.mu.Unlock()
	return true, nil
}

// Save enqueues a delayed SET write while holding the saver read lock.
func (s *SetSaver[T]) Save(ctx context.Context) error {
	s.mu.RLock()
	if s.IsDelete {
		s.mu.RUnlock()
		return nil
	}
	db, key, msg, ttl := s.db, s.DbKey, s.DbProto, s.ttl
	if err := validateSetBinding(db, key, msg); err != nil {
		s.mu.RUnlock()
		return err
	}
	err := db.enqueue(ctx, setSaveIntent(key, ttl, s))
	s.mu.RUnlock()
	return err
}

// SaveUnsafe enqueues a delayed SET write without acquiring the saver lock.
func (s *SetSaver[T]) SaveUnsafe(ctx context.Context) error {
	if s.IsDelete {
		return nil
	}
	if err := validateSetBinding(s.db, s.DbKey, s.DbProto); err != nil {
		return err
	}
	return s.db.enqueue(ctx, setSaveIntent(s.DbKey, s.ttl, s))
}

// Delete marks the saver deleted and removes the Redis key; it returns false when already deleted.
func (s *SetSaver[T]) Delete(ctx context.Context) bool {
	s.mu.Lock()
	if s.IsDelete || s.db == nil || s.db.store == nil || s.DbKey == "" {
		s.mu.Unlock()
		return false
	}
	db, key := s.db, s.DbKey
	if err := db.enqueue(ctx, setDeleteIntent(key)); err != nil {
		s.mu.Unlock()
		return false
	}
	s.IsDelete = true
	s.mu.Unlock()
	return true
}

// DeleteUnsafe deletes without acquiring the saver lock.
func (s *SetSaver[T]) DeleteUnsafe(ctx context.Context) bool {
	if s.IsDelete || s.db == nil || s.db.store == nil || s.DbKey == "" {
		return false
	}
	if err := s.db.enqueue(ctx, setDeleteIntent(s.DbKey)); err != nil {
		return false
	}
	s.IsDelete = true
	return true
}

// MarshalQueued returns a protobuf snapshot for the delayed worker.
func (s *SetSaver[T]) MarshalQueued() ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.IsDelete {
		return nil, false, nil
	}
	if isNilProtoMessage(s.DbProto) {
		return nil, false, ErrMessageMissing
	}
	raw, err := proto.Marshal(s.DbProto)
	return raw, err == nil, err
}

// CloneDbProto returns a deep copy of DbProto.
func (s *SetSaver[T]) CloneDbProto() T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneProtoMessage(s.DbProto)
}

// Lock acquires the saver write lock.
func (s *SetSaver[T]) Lock() {
	s.mu.Lock()
}

// Unlock releases the saver write lock.
func (s *SetSaver[T]) Unlock() {
	s.mu.Unlock()
}

// RLock acquires the saver read lock.
func (s *SetSaver[T]) RLock() {
	s.mu.RLock()
}

// RUnlock releases the saver read lock.
func (s *SetSaver[T]) RUnlock() {
	s.mu.RUnlock()
}

// HashSaver is embedded by business objects that persist one protobuf message under one Redis hash field.
type HashSaver[T proto.Message] struct {
	mu       sync.RWMutex
	db       *DB
	DbProto  T
	IsDelete bool
	DbKey    string
	field    string
	ttl      time.Duration
}

// Bind 把 saver 绑定到包级 DefaultDB 的一个 HASH field。
func (s *HashSaver[T]) Bind(key, field string, pb T, opts ...Option) error {
	return s.BindDB(DefaultDB(), key, field, pb, opts...)
}

// BindDB 把 saver 绑定到显式传入 DB 的一个 HASH field。
func (s *HashSaver[T]) BindDB(db *DB, key, field string, pb T, opts ...Option) error {
	if err := validateHashBinding(db, key, field, pb); err != nil {
		return err
	}
	cfg, err := applyOptions(opts)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.db = db
	s.DbKey = key
	s.field = field
	s.DbProto = pb
	s.ttl = cfg.ttl
	s.IsDelete = false
	return nil
}

// Key returns the Redis hash key owned by this saver.
func (s *HashSaver[T]) Key() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.DbKey
}

// Field returns the Redis hash field owned by this saver.
func (s *HashSaver[T]) Field() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.field
}

// Load replaces DbProto with the stored protobuf message when the field exists.
func (s *HashSaver[T]) Load(ctx context.Context) (bool, error) {
	s.mu.RLock()
	db, key, field, template := s.db, s.DbKey, s.field, s.DbProto
	s.mu.RUnlock()
	if err := validateHashBinding(db, key, field, template); err != nil {
		return false, err
	}
	raw, ok, err := db.store.HGet(ctx, key, field)
	if err != nil || !ok {
		return ok, err
	}
	msg, err := newProtoMessage(template)
	if err != nil {
		return false, err
	}
	if err := proto.Unmarshal(raw, msg); err != nil {
		return false, err
	}
	s.mu.Lock()
	s.DbProto = msg
	s.IsDelete = false
	s.mu.Unlock()
	return true, nil
}

// Save enqueues a delayed HSET write while holding the saver read lock.
func (s *HashSaver[T]) Save(ctx context.Context) error {
	s.mu.RLock()
	if s.IsDelete {
		s.mu.RUnlock()
		return nil
	}
	db, key, field, msg, ttl := s.db, s.DbKey, s.field, s.DbProto, s.ttl
	if err := validateHashBinding(db, key, field, msg); err != nil {
		s.mu.RUnlock()
		return err
	}
	err := db.enqueue(ctx, hashSaveIntent(key, field, ttl, s))
	s.mu.RUnlock()
	return err
}

// SaveUnsafe enqueues a delayed HSET write without acquiring the saver lock.
func (s *HashSaver[T]) SaveUnsafe(ctx context.Context) error {
	if s.IsDelete {
		return nil
	}
	if err := validateHashBinding(s.db, s.DbKey, s.field, s.DbProto); err != nil {
		return err
	}
	return s.db.enqueue(ctx, hashSaveIntent(s.DbKey, s.field, s.ttl, s))
}

// Delete marks the saver deleted and removes the Redis hash field; it returns false when already deleted.
func (s *HashSaver[T]) Delete(ctx context.Context) bool {
	s.mu.Lock()
	if s.IsDelete || s.db == nil || s.db.store == nil || s.DbKey == "" || s.field == "" {
		s.mu.Unlock()
		return false
	}
	db, key, field := s.db, s.DbKey, s.field
	if err := db.enqueue(ctx, hashDeleteIntent(key, field)); err != nil {
		s.mu.Unlock()
		return false
	}
	s.IsDelete = true
	s.mu.Unlock()
	return true
}

// DeleteUnsafe deletes without acquiring the saver lock.
func (s *HashSaver[T]) DeleteUnsafe(ctx context.Context) bool {
	if s.IsDelete || s.db == nil || s.db.store == nil || s.DbKey == "" || s.field == "" {
		return false
	}
	if err := s.db.enqueue(ctx, hashDeleteIntent(s.DbKey, s.field)); err != nil {
		return false
	}
	s.IsDelete = true
	return true
}

// MarshalQueued returns a protobuf snapshot for the delayed worker.
func (s *HashSaver[T]) MarshalQueued() ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.IsDelete {
		return nil, false, nil
	}
	if isNilProtoMessage(s.DbProto) {
		return nil, false, ErrMessageMissing
	}
	raw, err := proto.Marshal(s.DbProto)
	return raw, err == nil, err
}

// CloneDbProto returns a deep copy of DbProto.
func (s *HashSaver[T]) CloneDbProto() T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneProtoMessage(s.DbProto)
}

// Lock acquires the saver write lock.
func (s *HashSaver[T]) Lock() {
	s.mu.Lock()
}

// Unlock releases the saver write lock.
func (s *HashSaver[T]) Unlock() {
	s.mu.Unlock()
}

// RLock acquires the saver read lock.
func (s *HashSaver[T]) RLock() {
	s.mu.RLock()
}

// RUnlock releases the saver read lock.
func (s *HashSaver[T]) RUnlock() {
	s.mu.RUnlock()
}

func validateSetBinding[T proto.Message](db *DB, key string, msg T) error {
	if err := db.validate(); err != nil {
		return err
	}
	if key == "" {
		return ErrKeyMissing
	}
	if isNilProtoMessage(msg) {
		return ErrMessageMissing
	}
	return nil
}

func validateHashBinding[T proto.Message](db *DB, key, field string, msg T) error {
	if err := validateSetBinding(db, key, msg); err != nil {
		return err
	}
	if field == "" {
		return ErrFieldMissing
	}
	return nil
}
