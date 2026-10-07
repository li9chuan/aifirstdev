package root

import (
	"context"
	"fmt"
	"sync"

	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	"google.golang.org/protobuf/proto"
)

// Root wraps a business root proto with concurrency control and rdb persistence.
type Root[T proto.Message] struct {
	mu    sync.RWMutex
	id    int64
	saver *rdb.SetSaver[T]
}

func newRoot[T proto.Message](rootID int64, saver *rdb.SetSaver[T]) *Root[T] {
	return &Root[T]{id: rootID, saver: saver}
}

// ID returns the stable root identifier.
func (o *Root[T]) ID() int64 {
	if o == nil {
		return 0
	}
	return o.id
}

// Read exposes the root proto to a read-only callback while holding the read lock.
func (o *Root[T]) Read(fn func(T)) error {
	if o == nil {
		return fmt.Errorf("root is nil")
	}
	if fn == nil {
		return fmt.Errorf("root read callback is nil")
	}
	o.mu.RLock()
	o.saver.RLock()
	db := o.saver.DbProto
	fn(db)
	o.saver.RUnlock()
	o.mu.RUnlock()
	return nil
}

// Mutate edits the root proto under the write lock and queues the full document for rdb persistence.
func (o *Root[T]) Mutate(ctx context.Context, fn func(T) error) error {
	if o == nil {
		return fmt.Errorf("root is nil")
	}
	if fn == nil {
		return fmt.Errorf("root mutate callback is nil")
	}
	o.mu.Lock()
	o.saver.Lock()
	defer o.saver.Unlock()
	defer o.mu.Unlock()
	if err := fn(o.saver.DbProto); err != nil {
		return err
	}
	return o.saver.SaveUnsafe(ctx)
}

// Save queues the current root proto for rdb persistence.
func (o *Root[T]) Save(ctx context.Context) error {
	if o == nil {
		return fmt.Errorf("root is nil")
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.saver.Save(ctx)
}

// Snapshot returns a deep copy of the root proto.
func (o *Root[T]) Snapshot() T {
	if o == nil {
		var zero T
		return zero
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.saver.CloneDbProto()
}

// StoreOptions configures the business root document store.
type StoreOptions[T proto.Message] struct {
	DB      *rdb.DB
	Key     func(rootID int64) string
	New     func(rootID int64) T
	Options []rdb.Option
}

// Store loads or creates root protos by root ID using rdb.SetSaver.
type Store[T proto.Message] struct {
	opts StoreOptions[T]
}

// NewStore creates a root document store.
func NewStore[T proto.Message](opts StoreOptions[T]) (*Store[T], error) {
	if opts.Key == nil {
		return nil, fmt.Errorf("root store key function is nil")
	}
	if opts.New == nil {
		return nil, fmt.Errorf("root store new function is nil")
	}
	return &Store[T]{opts: opts}, nil
}

// LoadOrNew loads a root document from rdb, or creates and queues a new document when missing.
func (s *Store[T]) LoadOrNew(ctx context.Context, rootID int64) (*Root[T], bool, error) {
	if s == nil {
		return nil, false, fmt.Errorf("root store is nil")
	}
	if rootID <= 0 {
		return nil, false, fmt.Errorf("root id must be positive")
	}
	db := s.opts.New(rootID)
	saver := &rdb.SetSaver[T]{}
	if err := s.bindSaver(saver, rootID, db); err != nil {
		return nil, false, err
	}
	ok, err := saver.Load(ctx)
	if err != nil {
		return nil, false, err
	}
	root := newRoot(rootID, saver)
	if ok {
		return root, false, nil
	}
	if err := root.Save(ctx); err != nil {
		return nil, false, err
	}
	return root, true, nil
}

func (s *Store[T]) bindSaver(saver *rdb.SetSaver[T], rootID int64, db T) error {
	if s.opts.DB != nil {
		return saver.BindDB(s.opts.DB, s.opts.Key(rootID), db, s.opts.Options...)
	}
	return saver.Bind(s.opts.Key(rootID), db, s.opts.Options...)
}
