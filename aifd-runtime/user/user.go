package user

import (
	"context"
	"errors"
	"fmt"
	"sync"

	rdb "github.com/li9chuan/aifirstdev/aifd-rdb"
	"google.golang.org/protobuf/proto"
)

// User owns one user's typed document and coordinates its business data blocks.
// The user lock precedes every block saver lock. It does not provide a database transaction.
type User[T proto.Message] struct {
	mu    sync.RWMutex
	uid   int64
	saver *rdb.SetSaver[T]
}

func newUser[T proto.Message](uid int64, saver *rdb.SetSaver[T]) *User[T] {
	return &User[T]{uid: uid, saver: saver}
}

// UID returns the stable user identity.
func (o *User[T]) UID() int64 {
	if o == nil {
		return 0
	}
	return o.uid
}

// RLock coordinates a consistent read across user-owned blocks.
// Do not call Read, Mutate, Save or Snapshot until RUnlock.
func (o *User[T]) RLock() { o.mu.RLock() }

// RUnlock releases the user coordination read lock.
func (o *User[T]) RUnlock() { o.mu.RUnlock() }

// Lock coordinates changes across user-owned blocks. Acquire saver locks afterwards.
// Do not call Read, Mutate, Save or Snapshot until Unlock.
func (o *User[T]) Lock() { o.mu.Lock() }

// Unlock releases the user coordination write lock.
func (o *User[T]) Unlock() { o.mu.Unlock() }

// SaverUnsafe exposes the primary typed block to business storage adapters only.
// The caller must hold the user lock, then acquire the saver lock before accessing
// DbProto. SaveUnsafe additionally requires the saver write lock. Ordinary handlers
// use Read, Mutate or Snapshot instead. Returned references must not escape the lock.
func (o *User[T]) SaverUnsafe() *rdb.SetSaver[T] { return o.saver }

// Read exposes the primary proto to a read-only callback while holding the read lock.
func (o *User[T]) Read(fn func(T)) error {
	if o == nil {
		return fmt.Errorf("user is nil")
	}
	if fn == nil {
		return fmt.Errorf("user read callback is nil")
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	o.saver.RLock()
	defer o.saver.RUnlock()
	db := o.saver.DbProto
	fn(db)
	return nil
}

// Mutate edits a private candidate and queues it for persistence. A callback error,
// panic or rejected save leaves the published document unchanged. The callback must
// be short, must not retain the candidate, and must not recursively lock the user.
func (o *User[T]) Mutate(ctx context.Context, fn func(T) error) error {
	if o == nil {
		return fmt.Errorf("user is nil")
	}
	if fn == nil {
		return fmt.Errorf("user mutate callback is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	o.mu.Lock()
	o.saver.Lock()
	defer o.saver.Unlock()
	defer o.mu.Unlock()
	candidate := proto.Clone(o.saver.DbProto).(T)
	if err := fn(candidate); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if proto.Equal(candidate, o.saver.DbProto) {
		return nil
	}
	previous := o.saver.DbProto
	o.saver.DbProto = candidate
	if err := o.saver.SaveUnsafe(ctx); err != nil {
		o.saver.DbProto = previous
		return err
	}
	return nil
}

// Save queues the current primary proto for rdb persistence.
func (o *User[T]) Save(ctx context.Context) error {
	if o == nil {
		return fmt.Errorf("user is nil")
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.saver.Save(ctx)
}

// Snapshot returns a deep copy of the primary proto.
func (o *User[T]) Snapshot() T {
	if o == nil {
		var zero T
		return zero
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.saver.CloneDbProto()
}

var (
	// ErrNotFound means no existing user document was found; Load never creates one.
	ErrNotFound = errors.New("user does not exist")
	// ErrAlreadyExists rejects an explicit create for an existing user.
	ErrAlreadyExists = errors.New("user already exists")
)

// StoreOptions configures the business user document store; keys remain business-owned.
type StoreOptions[T proto.Message] struct {
	DB      *rdb.DB
	Key     func(uid int64) string
	New     func(uid int64) T
	Options []rdb.Option
	// Exists optionally verifies a user in a business-owned identity source when
	// its primary PB is absent. Loading that user keeps the default PB in memory
	// without writing a document. Nil means the primary document defines existence.
	Exists func(context.Context, int64) (bool, error)
}

// Store reads and explicitly creates typed user documents using rdb.SetSaver.
type Store[T proto.Message] struct {
	opts StoreOptions[T]
}

// NewStore creates a user document store.
func NewStore[T proto.Message](opts StoreOptions[T]) (*Store[T], error) {
	if opts.Key == nil {
		return nil, fmt.Errorf("user store key function is nil")
	}
	if opts.New == nil {
		return nil, fmt.Errorf("user store new function is nil")
	}
	return &Store[T]{opts: opts}, nil
}

// Load reads an existing document without creating or saving a missing user.
func (s *Store[T]) Load(ctx context.Context, uid int64) (*User[T], error) {
	owner, found, err := s.read(ctx, uid)
	if err != nil {
		return nil, err
	}
	if !found {
		if s.opts.Exists == nil {
			return nil, ErrNotFound
		}
		known, err := s.opts.Exists(ctx, uid)
		if err != nil {
			return nil, err
		}
		if !known {
			return nil, ErrNotFound
		}
	}
	return owner, nil
}

// Create explicitly initializes a missing user. The owning runtime serializes
// creates by UID; applications must keep the same UID assigned to one owner.
func (s *Store[T]) Create(ctx context.Context, uid int64) (*User[T], error) {
	owner, found, err := s.read(ctx, uid)
	if err != nil {
		return nil, err
	}
	if found {
		return nil, ErrAlreadyExists
	}
	if s.opts.Exists != nil {
		known, err := s.opts.Exists(ctx, uid)
		if err != nil {
			return nil, err
		}
		if known {
			return nil, ErrAlreadyExists
		}
	}
	if err := owner.Save(ctx); err != nil {
		return nil, err
	}
	return owner, nil
}

func (s *Store[T]) read(ctx context.Context, uid int64) (*User[T], bool, error) {
	if s == nil {
		return nil, false, fmt.Errorf("user store is nil")
	}
	if uid <= 0 {
		return nil, false, fmt.Errorf("user UID must be positive")
	}
	db := s.opts.New(uid)
	saver := &rdb.SetSaver[T]{}
	if err := s.bindSaver(saver, uid, db); err != nil {
		return nil, false, err
	}
	ok, err := saver.Load(ctx)
	if err != nil {
		return nil, false, err
	}
	return newUser(uid, saver), ok, nil
}

func (s *Store[T]) bindSaver(saver *rdb.SetSaver[T], uid int64, db T) error {
	if s.opts.DB != nil {
		return saver.BindDB(s.opts.DB, s.opts.Key(uid), db, s.opts.Options...)
	}
	return saver.Bind(s.opts.Key(uid), db, s.opts.Options...)
}
