package rdb

import (
	"context"
	"errors"
	"sync"
	"time"

	observe "github.com/li9chuan/aifirstdev/aifd-observe/contracts"
	redisgo "github.com/redis/go-redis/v9"
)

const (
	defaultMinSaveDelay          = 20 * time.Millisecond
	defaultMaxSaveDelay          = 30 * time.Second
	defaultMinRetryDelay         = time.Second
	defaultMaxRetryDelay         = 30 * time.Second
	defaultLargeSetPayloadBytes  = 2 * 1024 * 1024
	defaultLargeHashPayloadBytes = 1024 * 1024
	workerTickInterval           = 10 * time.Millisecond
)

var (
	defaultDBMu sync.RWMutex
	defaultDB   *DB
)

type retryState struct {
	op         finalIntent
	attempts   int
	retryAfter time.Time
	lastErr    error
}

// Failed writes stay unresolved, but are retried only by an explicit Flush.
type failedWrite struct {
	op       finalIntent
	attempts int
	err      error
}

// DB owns the narrow Set/HSet storage backend and delayed save queue used by mixin savers.
type DB struct {
	store store

	queueMu  sync.Mutex
	wait     *waitList
	retrying *retryState
	failed   map[entityKey]failedWrite

	minDelay              time.Duration
	maxDelay              time.Duration
	saveStayMin           time.Duration
	minRetryDelay         time.Duration
	maxRetryDelay         time.Duration
	failureFileDir        string
	errorHandler          ErrorHandler
	observeSink           observe.Sink
	observeRawKeys        bool
	largeSetPayloadBytes  int
	largeHashPayloadBytes int

	lastSave      time.Time
	wake          chan struct{}
	stop          chan struct{}
	stopOnce      sync.Once
	workerDone    chan struct{}
	workerStarted bool
	drainMu       sync.Mutex

	stateMu sync.Mutex
	closing bool
	closed  bool
}

// NewMemoryDB creates an in-memory DB with the same delayed-save behavior as Redis DB.
func NewMemoryDB(opts ...Option) (*DB, error) {
	cfg, err := applyDBOptions(opts)
	if err != nil {
		return nil, err
	}
	db := newDB(newMemoryStore(), cfg)
	db.startWorker()
	return db, nil
}

// MustNewMemoryDB creates an in-memory DB or panics. It is intended for tests and explicit fail-fast wiring.
func MustNewMemoryDB(opts ...Option) *DB {
	db, err := NewMemoryDB(opts...)
	if err != nil {
		panic(err)
	}
	return db
}

// NewRedisDB wraps a caller-owned Redis client without taking over its lifecycle.
func NewRedisDB(client redisgo.UniversalClient, opts ...Option) (*DB, error) {
	if client == nil {
		return nil, ErrRedisClientMissing
	}
	cfg, err := applyDBOptions(opts)
	if err != nil {
		return nil, err
	}
	db := newDB(&redisStore{client: client}, cfg)
	if cfg.startupReplay {
		if err := replayStartupJournals(context.Background(), db.store, db.failureFileDir); err != nil {
			return nil, err
		}
	}
	db.startWorker()
	return db, nil
}

// DefaultDB returns the package-level DB used by saver Bind.
func DefaultDB() *DB {
	defaultDBMu.RLock()
	defer defaultDBMu.RUnlock()
	return defaultDB
}

// SetDefaultDB installs the package-level DB used by saver Bind.
func SetDefaultDB(db *DB) {
	defaultDBMu.Lock()
	defaultDB = db
	defaultDBMu.Unlock()
}

// FlushDefaultDB forces all pending writes on the package-level DB to complete.
func FlushDefaultDB(ctx context.Context) error {
	db := DefaultDB()
	if db == nil {
		return ErrDBMissing
	}
	return db.Flush(ctx)
}

// CloseDefaultDB flushes and stops the package-level DB worker.
func CloseDefaultDB(ctx context.Context) error {
	db := DefaultDB()
	if db == nil {
		return ErrDBMissing
	}
	err := db.Close(ctx)
	defaultDBMu.Lock()
	if defaultDB == db {
		defaultDB = nil
	}
	defaultDBMu.Unlock()
	return err
}

func newDB(store store, cfg options) *DB {
	return &DB{
		store:                 store,
		wait:                  newWaitList(),
		failed:                make(map[entityKey]failedWrite),
		minDelay:              cfg.minSaveDelay,
		maxDelay:              cfg.maxSaveDelay,
		saveStayMin:           cfg.saveStayMin,
		minRetryDelay:         cfg.minRetryDelay,
		maxRetryDelay:         cfg.maxRetryDelay,
		failureFileDir:        cfg.failureFileDir,
		errorHandler:          cfg.errorHandler,
		observeSink:           cfg.observeSink,
		observeRawKeys:        cfg.observeRawKeys,
		largeSetPayloadBytes:  cfg.largeSetPayloadBytes,
		largeHashPayloadBytes: cfg.largeHashPayloadBytes,
		lastSave:              time.Now(),
		wake:                  make(chan struct{}, 1),
		stop:                  make(chan struct{}),
		workerDone:            make(chan struct{}),
	}
}

func applyDBOptions(opts []Option) (options, error) {
	cfg := options{
		minSaveDelay:          defaultMinSaveDelay,
		maxSaveDelay:          defaultMaxSaveDelay,
		minRetryDelay:         defaultMinRetryDelay,
		maxRetryDelay:         defaultMaxRetryDelay,
		failureFileDir:        defaultFailureFileDir,
		largeSetPayloadBytes:  defaultLargeSetPayloadBytes,
		largeHashPayloadBytes: defaultLargeHashPayloadBytes,
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&cfg); err != nil {
			return options{}, err
		}
	}
	return cfg, nil
}

func (db *DB) validate() error {
	if db == nil || db.store == nil {
		return ErrDBMissing
	}
	return nil
}

func (db *DB) enqueue(ctx context.Context, ops ...finalIntent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := db.validate(); err != nil {
		return err
	}
	db.stateMu.Lock()
	if db.closing || db.closed {
		db.stateMu.Unlock()
		return ErrDBClosing
	}
	db.stateMu.Unlock()

	db.queueMu.Lock()
	for _, op := range ops {
		if op.key == "" {
			continue
		}
		if db.retrying != nil && db.retrying.op.entityKey() == op.entityKey() {
			db.retrying = nil
		}
		delete(db.failed, op.entityKey())
		db.wait.PushBack(op)
	}
	waitSize := db.wait.Len() + len(db.failed)
	if db.retrying != nil {
		waitSize++
	}
	db.queueMu.Unlock()
	db.reportQueueDepth(ctx, waitSize)
	db.notifyWorker()
	return nil
}

// WaitSize returns the number of unresolved final intents.
func (db *DB) WaitSize() int {
	if db == nil || db.wait == nil {
		return 0
	}
	db.queueMu.Lock()
	defer db.queueMu.Unlock()
	size := db.wait.Len() + len(db.failed)
	if db.retrying != nil {
		size++
	}
	return size
}

// Flush retries previously failed writes once, then drains pending final intents.
// Any unresolved failure remains observable and prevents a successful Flush.
func (db *DB) Flush(ctx context.Context) error {
	if err := db.validate(); err != nil {
		return err
	}
	db.drainMu.Lock()
	defer db.drainMu.Unlock()
	if err := db.flushFailed(ctx); err != nil {
		return errors.Join(err, db.failedErrors())
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		op, attempts, ok := db.takeNext(true)
		if !ok {
			return db.failedErrors()
		}
		if err := db.applyFinalIntent(ctx, "flush", op, attempts); err != nil {
			if isRetryableError(err) {
				db.installRetry(ctx, "flush", op, attempts, err)
				return errors.Join(err, db.failedErrors())
			}
			db.reportError(ctx, "flush", op, attempts, false, false, err)
			db.retainFailed(op, attempts, err)
		}
	}
}

// Close rejects new writes, writes final retryable failures to journal, and stops the worker.
func (db *DB) Close(ctx context.Context) error {
	if err := db.validate(); err != nil {
		return err
	}
	db.stateMu.Lock()
	if db.closed {
		db.stateMu.Unlock()
		return db.failedErrors()
	}
	db.closing = true
	db.stateMu.Unlock()

	err := db.flushForClose()
	db.stopOnce.Do(func() {
		close(db.stop)
	})
	db.stateMu.Lock()
	workerStarted := db.workerStarted
	db.stateMu.Unlock()
	if workerStarted {
		<-db.workerDone
	}

	db.stateMu.Lock()
	db.closed = true
	db.stateMu.Unlock()
	return errors.Join(err, db.failedErrors())
}

func (db *DB) flushForClose() error {
	db.drainMu.Lock()
	defer db.drainMu.Unlock()
	for {
		op, attempts, ok := db.takeNext(true)
		if !ok {
			return nil
		}
		if err := db.applyFinalIntent(context.Background(), "close", op, attempts); err != nil {
			if isRetryableError(err) {
				journalCount, journalErr := db.writeCloseJournal(op, attempts+1, err)
				if journalErr != nil {
					db.restoreRetry(op, attempts+1, err)
					db.reportError(context.Background(), "close-journal", op, attempts+1, true, true, journalErr)
					return errors.Join(err, journalErr)
				}
				db.reportCloseJournal(context.Background(), journalCount)
				db.clearQueue()
				db.reportError(context.Background(), "close", op, attempts+1, true, true, err)
				return err
			}
			db.reportError(context.Background(), "close", op, attempts, false, true, err)
			db.retainFailed(op, attempts, err)
		}
	}
}

func (db *DB) startWorker() {
	db.stateMu.Lock()
	if db.workerStarted {
		db.stateMu.Unlock()
		return
	}
	db.workerStarted = true
	db.stateMu.Unlock()
	go db.runWorker()
}

func (db *DB) runWorker() {
	defer close(db.workerDone)
	ticker := time.NewTicker(workerTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-db.stop:
			return
		case <-db.wake:
			db.drainDue(context.Background())
		case <-ticker.C:
			db.drainDue(context.Background())
		}
	}
}

func (db *DB) drainDue(ctx context.Context) {
	db.drainMu.Lock()
	defer db.drainMu.Unlock()
	op, attempts, ok := db.takeNext(false)
	if !ok {
		return
	}
	db.lastSave = time.Now()
	if err := db.applyFinalIntent(ctx, "worker", op, attempts); err != nil {
		if isRetryableError(err) {
			db.installRetry(ctx, "worker", op, attempts, err)
			return
		}
		db.reportError(ctx, "worker", op, attempts, false, false, err)
		db.retainFailed(op, attempts, err)
	}
}

func (db *DB) takeNext(force bool) (finalIntent, int, bool) {
	db.queueMu.Lock()
	defer db.queueMu.Unlock()
	now := time.Now()
	if db.retrying != nil {
		retry := db.retrying
		if !force && now.Before(retry.retryAfter) {
			return finalIntent{}, 0, false
		}
		db.retrying = nil
		return retry.op, retry.attempts, true
	}
	if !force && !db.shouldDrainLocked(now) {
		return finalIntent{}, 0, false
	}
	var (
		op finalIntent
		ok bool
	)
	if !force && db.saveStayMin > 0 {
		op, ok = db.wait.PopFrontReady(now, db.saveStayMin)
	} else {
		op, ok = db.wait.PopFront()
	}
	return op, 0, ok
}

func (db *DB) shouldDrainLocked(now time.Time) bool {
	waitSize := db.wait.Len()
	if waitSize <= 0 {
		return false
	}
	delay := db.maxDelay / time.Duration(waitSize)
	if delay < db.minDelay {
		delay = db.minDelay
	}
	return now.Sub(db.lastSave) >= delay
}

func (db *DB) installRetry(ctx context.Context, stage string, op finalIntent, attempts int, err error) {
	nextAttempts := attempts + 1
	db.reportError(ctx, stage, op, nextAttempts, true, false, err)
	db.queueMu.Lock()
	if !db.wait.Has(op.entityKey()) {
		db.retrying = &retryState{
			op:         op,
			attempts:   nextAttempts,
			retryAfter: time.Now().Add(db.retryDelay(nextAttempts)),
			lastErr:    err,
		}
	}
	db.queueMu.Unlock()
}

func (db *DB) restoreRetry(op finalIntent, attempts int, err error) {
	db.queueMu.Lock()
	db.retrying = &retryState{
		op:         op,
		attempts:   attempts,
		retryAfter: time.Now().Add(db.retryDelay(attempts)),
		lastErr:    err,
	}
	db.queueMu.Unlock()
}

func (db *DB) retryDelay(attempts int) time.Duration {
	if attempts <= 1 {
		return db.minRetryDelay
	}
	delay := db.minRetryDelay
	for i := 1; i < attempts; i++ {
		if delay >= db.maxRetryDelay/2 {
			return db.maxRetryDelay
		}
		delay *= 2
	}
	if delay > db.maxRetryDelay {
		return db.maxRetryDelay
	}
	return delay
}

func (db *DB) writeCloseJournal(first finalIntent, attempts int, cause error) (int, error) {
	db.queueMu.Lock()
	ops := append([]finalIntent{first}, db.wait.Snapshot()...)
	db.queueMu.Unlock()

	records := make([]journalRecord, 0, len(ops))
	for i, op := range ops {
		recordAttempts := 0
		var recordErr error
		if i == 0 {
			recordAttempts = attempts
			recordErr = cause
		}
		record, ok, err := op.materialize(journalSourceClose, recordAttempts, recordErr)
		if errors.Is(err, ErrJournalPayloadMissing) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if ok {
			records = append(records, record)
		}
	}
	return len(records), writeJournalFile(db.failureFileDir, journalSourceClose, records)
}

func (db *DB) clearQueue() {
	db.queueMu.Lock()
	db.retrying = nil
	db.wait.Clear()
	db.queueMu.Unlock()
}

func (db *DB) notifyWorker() {
	select {
	case db.wake <- struct{}{}:
	default:
	}
}

func (db *DB) reportError(ctx context.Context, stage string, op finalIntent, attempts int, retryable, final bool, err error) {
	if err == nil {
		return
	}
	if db.errorHandler == nil {
		db.reportSaveError(ctx, stage, op, attempts, retryable, final)
		return
	}
	db.errorHandler(ctx, ErrorEvent{
		Stage:     stage,
		Kind:      op.kind,
		Intent:    op.intent,
		Key:       op.key,
		Field:     op.field,
		Attempts:  attempts,
		Retryable: retryable,
		Final:     final,
		Err:       err,
	})
	db.reportSaveError(ctx, stage, op, attempts, retryable, final)
}

// A newer queued intent supersedes the failure of an in-flight older write.
func (db *DB) retainFailed(op finalIntent, attempts int, err error) {
	db.queueMu.Lock()
	defer db.queueMu.Unlock()
	if !db.wait.Has(op.entityKey()) {
		db.failed[op.entityKey()] = failedWrite{op: op, attempts: attempts, err: err}
	}
}

func (db *DB) failedErrors() error {
	db.queueMu.Lock()
	defer db.queueMu.Unlock()
	errs := make([]error, 0, len(db.failed))
	for _, failed := range db.failed {
		errs = append(errs, failed.err)
	}
	return errors.Join(errs...)
}

// Caller holds drainMu. Snapshot keys so a newly failed intent is not retried
// again in this Flush; enqueue may replace one with a newer Save/Delete meanwhile.
func (db *DB) flushFailed(ctx context.Context) error {
	db.queueMu.Lock()
	keys := make([]entityKey, 0, len(db.failed))
	for key := range db.failed {
		keys = append(keys, key)
	}
	db.queueMu.Unlock()
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		db.queueMu.Lock()
		failed, ok := db.failed[key]
		delete(db.failed, key)
		db.queueMu.Unlock()
		if !ok {
			continue
		}
		attempts := failed.attempts + 1
		if err := db.applyFinalIntent(ctx, "flush", failed.op, attempts); err != nil {
			retryable := isRetryableError(err)
			db.reportError(ctx, "flush", failed.op, attempts, retryable, false, err)
			db.retainFailed(failed.op, attempts, err)
			// Explicitly retried failures stay separate from the one automatic
			// retry slot, which may already belong to a different entity.
			if retryable {
				return err
			}
		}
	}
	return nil
}
