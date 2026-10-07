package rdb

import (
	"context"
	"time"

	observe "github.com/li9chuan/aifirstdev/aifd-observe/contracts"
)

const keepTTL = time.Duration(-1)

type options struct {
	ttl                   time.Duration
	minSaveDelay          time.Duration
	maxSaveDelay          time.Duration
	saveStayMin           time.Duration
	minRetryDelay         time.Duration
	maxRetryDelay         time.Duration
	failureFileDir        string
	startupReplay         bool
	errorHandler          ErrorHandler
	observeSink           observe.Sink
	observeRawKeys        bool
	largeSetPayloadBytes  int
	largeHashPayloadBytes int
}

// Option configures saver persistence behavior.
type Option func(*options) error

// ErrorEvent describes a failed final-intent write without exposing protobuf payloads.
type ErrorEvent struct {
	Stage     string
	Kind      string
	Intent    string
	Key       string
	Field     string
	Attempts  int
	Retryable bool
	Final     bool
	Err       error
}

// ErrorHandler receives immediate write and journal errors.
type ErrorHandler func(context.Context, ErrorEvent)

// WithTTL makes Set writes expire the key and HashSaver writes refresh the whole hash key.
func WithTTL(ttl time.Duration) Option {
	return func(o *options) error {
		if ttl <= 0 {
			return ErrInvalidTTL
		}
		o.ttl = ttl
		return nil
	}
}

// KeepTTL makes Set writes preserve an existing TTL; HashSaver writes already preserve hash TTL by default.
func KeepTTL() Option {
	return func(o *options) error {
		o.ttl = keepTTL
		return nil
	}
}

// WithSaveDelay configures normal delayed-save pacing; Flush and Close always drain immediately.
func WithSaveDelay(minDelay, maxDelay time.Duration) Option {
	return func(o *options) error {
		if minDelay < 0 || maxDelay <= 0 || minDelay > maxDelay {
			return ErrInvalidSaveDelay
		}
		o.minSaveDelay = minDelay
		o.maxSaveDelay = maxDelay
		return nil
	}
}

// WithSaveStayMin configures how long one final intent must stay queued before normal worker drain.
// Pass 0 to disable the minimum stay window; Flush and Close always drain immediately.
func WithSaveStayMin(d time.Duration) Option {
	return func(o *options) error {
		if d < 0 {
			return ErrInvalidSaveDelay
		}
		o.saveStayMin = d
		return nil
	}
}

// WithRetryDelay configures retry backoff for Redis connection failures.
func WithRetryDelay(minDelay, maxDelay time.Duration) Option {
	return func(o *options) error {
		if minDelay < 0 || maxDelay <= 0 || minDelay > maxDelay {
			return ErrInvalidRetryDelay
		}
		o.minRetryDelay = minDelay
		o.maxRetryDelay = maxDelay
		return nil
	}
}

// WithFailureFileDir configures where Close writes failed final-intent journals.
func WithFailureFileDir(dir string) Option {
	return func(o *options) error {
		if dir == "" {
			return ErrFailureFileDirMissing
		}
		o.failureFileDir = dir
		return nil
	}
}

// WithStartupReplay replays close journals during NewRedisDB before the worker starts.
func WithStartupReplay() Option {
	return func(o *options) error {
		o.startupReplay = true
		return nil
	}
}

// WithErrorHandler installs a best-effort callback for write, journal, and replay failures.
func WithErrorHandler(handler ErrorHandler) Option {
	return func(o *options) error {
		o.errorHandler = handler
		return nil
	}
}

// WithObserveSink 安装 rdb 运行指标出口；sink 写入失败会被忽略，不影响保存流程。
func WithObserveSink(sink observe.Sink) Option {
	return func(o *options) error {
		o.observeSink = sink
		return nil
	}
}

// WithObserveRawKeys 允许 observe 指标携带原始 Redis key/field，仅用于本地排障。
func WithObserveRawKeys() Option {
	return func(o *options) error {
		o.observeRawKeys = true
		return nil
	}
}

// WithLargePayloadThresholds 配置大 payload 告警阈值；传 0 表示关闭对应告警。
func WithLargePayloadThresholds(setBytes, hashFieldBytes int) Option {
	return func(o *options) error {
		if setBytes < 0 || hashFieldBytes < 0 {
			return ErrInvalidLargePayloadThreshold
		}
		o.largeSetPayloadBytes = setBytes
		o.largeHashPayloadBytes = hashFieldBytes
		return nil
	}
}

func applyOptions(opts []Option) (options, error) {
	var out options
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&out); err != nil {
			return options{}, err
		}
	}
	return out, nil
}
