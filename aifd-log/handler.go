package logging

import (
	"context"
	"errors"
	"log/slog"
)

var errLocalHandlerMissing = errors.New("logging local handler is missing")

type teeHandler struct {
	local     slog.Handler
	secondary slog.Handler
}

// NewTeeHandler creates a slog handler that writes local first and secondary best-effort.
func NewTeeHandler(local slog.Handler, secondary slog.Handler) (slog.Handler, error) {
	if local == nil {
		return nil, errLocalHandlerMissing
	}
	return &teeHandler{local: local, secondary: secondary}, nil
}

func (h *teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.local.Enabled(ctx, level)
}

func (h *teeHandler) Handle(ctx context.Context, record slog.Record) error {
	if err := h.local.Handle(ctx, record); err != nil {
		reportLocalError(err)
		return err
	}
	if h.secondary != nil && h.secondary.Enabled(ctx, record.Level) {
		_ = h.secondary.Handle(ctx, record)
	}
	return nil
}

func (h *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	var secondary slog.Handler
	if h.secondary != nil {
		secondary = h.secondary.WithAttrs(attrs)
	}
	return &teeHandler{local: h.local.WithAttrs(attrs), secondary: secondary}
}

func (h *teeHandler) WithGroup(name string) slog.Handler {
	var secondary slog.Handler
	if h.secondary != nil {
		secondary = h.secondary.WithGroup(name)
	}
	return &teeHandler{local: h.local.WithGroup(name), secondary: secondary}
}
