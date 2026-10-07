package logging

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/li9chuan/aifirstdev/aifd-observe/contracts"
)

var errLocalHandlerMissing = errors.New("logging local handler is missing")

type teeHandler struct {
	local   slog.Handler
	observe slog.Handler
}

// NewTeeHandler creates a slog handler that writes local first and observe best-effort.
func NewTeeHandler(local slog.Handler, observe slog.Handler) (slog.Handler, error) {
	if local == nil {
		return nil, errLocalHandlerMissing
	}
	return &teeHandler{local: local, observe: observe}, nil
}

func (h *teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.local.Enabled(ctx, level)
}

func (h *teeHandler) Handle(ctx context.Context, record slog.Record) error {
	if err := h.local.Handle(ctx, record); err != nil {
		return err
	}
	if h.observe != nil && h.observe.Enabled(ctx, record.Level) {
		_ = h.observe.Handle(ctx, record)
	}
	return nil
}

func (h *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	var observe slog.Handler
	if h.observe != nil {
		observe = h.observe.WithAttrs(attrs)
	}
	return &teeHandler{local: h.local.WithAttrs(attrs), observe: observe}
}

func (h *teeHandler) WithGroup(name string) slog.Handler {
	var observe slog.Handler
	if h.observe != nil {
		observe = h.observe.WithGroup(name)
	}
	return &teeHandler{local: h.local.WithGroup(name), observe: observe}
}

type sinkHandler struct {
	sink   contracts.Sink
	attrs  []groupedAttr
	groups []string
}

type groupedAttr struct {
	groups []string
	attr   slog.Attr
}

// NewSinkHandler adapts a slog record into observe log events.
func NewSinkHandler(sink contracts.Sink) slog.Handler {
	return &sinkHandler{sink: sink}
}

func (h *sinkHandler) Enabled(context.Context, slog.Level) bool {
	return h != nil && h.sink != nil
}

func (h *sinkHandler) Handle(ctx context.Context, record slog.Record) error {
	if h == nil || h.sink == nil {
		return nil
	}
	fields := make(map[string]any)
	for _, attr := range h.attrs {
		addAttr(fields, attr.groups, attr.attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		addAttr(fields, h.groups, attr)
		return true
	})
	event := contracts.Event{
		Kind:    contracts.KindLog,
		Level:   observeLevel(record.Level),
		Name:    eventName(fields),
		Message: record.Message,
		Time:    record.Time.UTC(),
		Fields:  fields,
	}
	if sourceService, ok := fields["source_service"].(string); ok {
		event.SourceService = sourceService
	}
	if sourceInstance, ok := fields["source_instance"].(string); ok {
		event.SourceInstance = sourceInstance
	}
	if traceID, ok := fields["trace_id"].(string); ok {
		event.TraceID = traceID
	}
	if shardKey, ok := fields["shard_key"].(string); ok {
		event.ShardKey = shardKey
	}
	return h.sink.Write(ctx, []contracts.Event{event})
}

func (h *sinkHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	next.attrs = append([]groupedAttr(nil), h.attrs...)
	for _, attr := range attrs {
		next.attrs = append(next.attrs, groupedAttr{
			groups: append([]string(nil), h.groups...),
			attr:   attr,
		})
	}
	return &next
}

func (h *sinkHandler) WithGroup(name string) slog.Handler {
	next := *h
	if name != "" {
		next.groups = append(append([]string(nil), h.groups...), name)
	}
	return &next
}

func observeLevel(level slog.Level) contracts.Level {
	switch {
	case level >= slog.LevelError:
		return contracts.LevelError
	case level >= slog.LevelWarn:
		return contracts.LevelWarn
	case level <= slog.LevelDebug:
		return contracts.LevelDebug
	default:
		return contracts.LevelInfo
	}
}

func eventName(fields map[string]any) string {
	if module, ok := fields["module"].(string); ok && module != "" {
		return module
	}
	return "log"
}

func addAttr(fields map[string]any, groups []string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}
	key := attr.Key
	if len(groups) > 0 {
		key = strings.Join(append(append([]string(nil), groups...), key), ".")
	}
	fields[key] = slogValue(attr.Value)
}

func slogValue(value slog.Value) any {
	switch value.Kind() {
	case slog.KindString:
		return value.String()
	case slog.KindInt64:
		return value.Int64()
	case slog.KindUint64:
		return value.Uint64()
	case slog.KindFloat64:
		return value.Float64()
	case slog.KindBool:
		return value.Bool()
	case slog.KindDuration:
		return value.Duration()
	case slog.KindTime:
		return value.Time()
	case slog.KindGroup:
		group := make(map[string]any)
		for _, attr := range value.Group() {
			addAttr(group, nil, attr)
		}
		return group
	case slog.KindAny:
		return value.Any()
	default:
		return value.String()
	}
}
