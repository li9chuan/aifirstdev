package rdb

import "errors"

var (
	// ErrDBMissing indicates that a saver was bound without a usable DB.
	ErrDBMissing = errors.New("rdb db is missing")
	// ErrKeyMissing indicates that a saver was bound without a Redis key.
	ErrKeyMissing = errors.New("rdb key is missing")
	// ErrFieldMissing indicates that a hash saver was bound without a hash field.
	ErrFieldMissing = errors.New("rdb hash field is missing")
	// ErrMessageMissing indicates that a saver was bound without a protobuf message.
	ErrMessageMissing = errors.New("rdb proto message is missing")
	// ErrRedisClientMissing indicates that NewRedisDB received a nil Redis client.
	ErrRedisClientMissing = errors.New("rdb redis client is missing")
	// ErrInvalidTTL indicates that WithTTL received a non-positive duration.
	ErrInvalidTTL = errors.New("rdb ttl must be positive")
	// ErrInvalidSaveDelay indicates that WithSaveDelay received an unusable delay range.
	ErrInvalidSaveDelay = errors.New("rdb save delay is invalid")
	// ErrInvalidRetryDelay indicates that WithRetryDelay received an unusable delay range.
	ErrInvalidRetryDelay = errors.New("rdb retry delay is invalid")
	// ErrFailureFileDirMissing indicates that WithFailureFileDir received an empty path.
	ErrFailureFileDirMissing = errors.New("rdb failure file dir is missing")
	// ErrDBClosing indicates that the DB is closing and no longer accepts new save requests.
	ErrDBClosing = errors.New("rdb db is closing")
	// ErrJournalPayloadMissing indicates that a final save intent cannot be materialized for journal replay.
	ErrJournalPayloadMissing = errors.New("rdb journal save payload is missing")
	// ErrJournalReplayInProgress indicates that a stale replay marker requires manual inspection.
	ErrJournalReplayInProgress = errors.New("rdb journal replay is already in progress")
	// ErrInvalidLargePayloadThreshold 表示大 payload 阈值配置为负数。
	ErrInvalidLargePayloadThreshold = errors.New("rdb large payload threshold is invalid")
)
