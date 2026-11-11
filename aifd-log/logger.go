package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
)

// Level configures the minimum emitted log level without exposing slog to callers.
type Level int

const (
	// LevelDebug enables debug logs.
	LevelDebug Level = -4
	// LevelInfo enables info logs and above.
	LevelInfo Level = 0
	// LevelWarn enables warn logs and above.
	LevelWarn Level = 4
	// LevelError enables error logs only.
	LevelError Level = 8
)

// Logger is the AIFD logging facade used by framework and business code.
type Logger interface {
	Debug(context.Context, string, ...Field)
	Info(context.Context, string, ...Field)
	Warn(context.Context, string, ...Field)
	Error(context.Context, string, ...Field)
	With(...Field) Logger
	Module(string) Logger
}

type slogLogger struct {
	logger *slog.Logger
}

var (
	defaultMu     sync.RWMutex
	defaultLogger Logger = NewSlogLogger(slog.NewTextHandler(io.Discard, nil))
)

// NewSlogLogger wraps a slog handler in the AIFD logging facade.
func NewSlogLogger(handler slog.Handler) Logger {
	if handler == nil {
		handler = slog.NewTextHandler(io.Discard, nil)
	}
	return &slogLogger{logger: slog.New(handler)}
}

// NewTeeLogger creates a local-first logger; secondary receives best-effort copies.
func NewTeeLogger(local slog.Handler, secondary slog.Handler) (Logger, error) {
	handler, err := NewTeeHandler(local, secondary)
	if err != nil {
		return nil, err
	}
	return NewSlogLogger(handler), nil
}

// SetDefault installs the process-wide logger used by package-level helpers.
func SetDefault(logger Logger) {
	if logger == nil {
		logger = NewSlogLogger(nil)
	}
	defaultMu.Lock()
	defaultLogger = logger
	defaultMu.Unlock()
}

// Default returns the process-wide logger.
func Default() Logger {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultLogger
}

// Debug writes a debug log through the process-wide logger.
func Debug(msg string, fields ...Field) {
	DebugContext(context.Background(), msg, fields...)
}

// Info writes an info log through the process-wide logger.
func Info(msg string, fields ...Field) {
	InfoContext(context.Background(), msg, fields...)
}

// Warn writes a warn log through the process-wide logger.
func Warn(msg string, fields ...Field) {
	WarnContext(context.Background(), msg, fields...)
}

// Error writes an error log through the process-wide logger.
func Error(msg string, fields ...Field) {
	ErrorContext(context.Background(), msg, fields...)
}

// DebugContext writes a debug log with an explicit context.
func DebugContext(ctx context.Context, msg string, fields ...Field) {
	Default().Debug(ctx, msg, fields...)
}

// InfoContext writes an info log with an explicit context.
func InfoContext(ctx context.Context, msg string, fields ...Field) {
	Default().Info(ctx, msg, fields...)
}

// WarnContext writes a warn log with an explicit context.
func WarnContext(ctx context.Context, msg string, fields ...Field) {
	Default().Warn(ctx, msg, fields...)
}

// ErrorContext writes an error log with an explicit context.
func ErrorContext(ctx context.Context, msg string, fields ...Field) {
	Default().Error(ctx, msg, fields...)
}

// Debugf writes a formatted debug log through the process-wide logger.
func Debugf(format string, args ...any) {
	Debug(fmt.Sprintf(format, args...))
}

// Infof writes a formatted info log through the process-wide logger.
func Infof(format string, args ...any) {
	Info(fmt.Sprintf(format, args...))
}

// Warnf writes a formatted warn log through the process-wide logger.
func Warnf(format string, args ...any) {
	Warn(fmt.Sprintf(format, args...))
}

// Errorf writes a formatted error log through the process-wide logger.
func Errorf(format string, args ...any) {
	Error(fmt.Sprintf(format, args...))
}

// DebugfContext writes a formatted debug log with an explicit context.
func DebugfContext(ctx context.Context, format string, args ...any) {
	DebugContext(ctx, fmt.Sprintf(format, args...))
}

// InfofContext writes a formatted info log with an explicit context.
func InfofContext(ctx context.Context, format string, args ...any) {
	InfoContext(ctx, fmt.Sprintf(format, args...))
}

// WarnfContext writes a formatted warn log with an explicit context.
func WarnfContext(ctx context.Context, format string, args ...any) {
	WarnContext(ctx, fmt.Sprintf(format, args...))
}

// ErrorfContext writes a formatted error log with an explicit context.
func ErrorfContext(ctx context.Context, format string, args ...any) {
	ErrorContext(ctx, fmt.Sprintf(format, args...))
}

func (l *slogLogger) Debug(ctx context.Context, msg string, fields ...Field) {
	l.log(ctx, slog.LevelDebug, msg, fields...)
}

func (l *slogLogger) Info(ctx context.Context, msg string, fields ...Field) {
	l.log(ctx, slog.LevelInfo, msg, fields...)
}

func (l *slogLogger) Warn(ctx context.Context, msg string, fields ...Field) {
	l.log(ctx, slog.LevelWarn, msg, fields...)
}

func (l *slogLogger) Error(ctx context.Context, msg string, fields ...Field) {
	l.log(ctx, slog.LevelError, msg, fields...)
}

func (l *slogLogger) With(fields ...Field) Logger {
	if l == nil || l.logger == nil {
		return NewSlogLogger(nil)
	}
	return &slogLogger{logger: l.logger.With(attrsAsAny(attrsFromFields(fields))...)}
}

func (l *slogLogger) Module(name string) Logger {
	return l.With(String("module", name))
}

func (l *slogLogger) log(ctx context.Context, level slog.Level, msg string, fields ...Field) {
	if ctx == nil {
		ctx = context.Background()
	}
	if l == nil || l.logger == nil {
		return
	}
	l.logger.LogAttrs(ctx, level, msg, attrsFromFields(fields)...)
}

func attrsAsAny(attrs []slog.Attr) []any {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		out = append(out, attr)
	}
	return out
}
