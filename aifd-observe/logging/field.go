package logging

import (
	"log/slog"
	"time"
)

// Field is a typed logging attribute; callers use helpers instead of importing slog directly.
type Field struct {
	attr slog.Attr
}

// String records a string field.
func String(key, value string) Field {
	return Field{attr: slog.String(key, value)}
}

// Int64 records an int64 field.
func Int64(key string, value int64) Field {
	return Field{attr: slog.Int64(key, value)}
}

// Int records an int field.
func Int(key string, value int) Field {
	return Field{attr: slog.Int(key, value)}
}

// Uint64 records a uint64 field.
func Uint64(key string, value uint64) Field {
	return Field{attr: slog.Uint64(key, value)}
}

// Float64 records a float64 field.
func Float64(key string, value float64) Field {
	return Field{attr: slog.Float64(key, value)}
}

// Bool records a bool field.
func Bool(key string, value bool) Field {
	return Field{attr: slog.Bool(key, value)}
}

// Time records a timestamp field.
func Time(key string, value time.Time) Field {
	return Field{attr: slog.Time(key, value)}
}

// Duration records a duration field.
func Duration(key string, value time.Duration) Field {
	return Field{attr: slog.Duration(key, value)}
}

// Err records an error under the stable "err" field.
func Err(err error) Field {
	if err == nil {
		return Field{attr: slog.Any("err", nil)}
	}
	return Field{attr: slog.String("err", err.Error())}
}

// Any records a reflected field for uncommon value types.
func Any(key string, value any) Field {
	return Field{attr: slog.Any(key, value)}
}

func attrsFromFields(fields []Field) []slog.Attr {
	if len(fields) == 0 {
		return nil
	}
	attrs := make([]slog.Attr, 0, len(fields))
	for _, field := range fields {
		attrs = append(attrs, field.attr)
	}
	return attrs
}

func fieldsMap(fields []Field) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]any, len(fields))
	for _, field := range fields {
		addAttr(out, nil, field.attr)
	}
	return out
}
