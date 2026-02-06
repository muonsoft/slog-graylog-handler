package graylog

import (
	"context"
	"log/slog"
)

// MultiHandler fans out log records to multiple handlers (e.g. stdout and Graylog).
// It implements slog.Handler.
type MultiHandler struct {
	handlers []slog.Handler
}

// NewMultiHandler returns a handler that sends each record to all of the given handlers.
func NewMultiHandler(handlers ...slog.Handler) *MultiHandler {
	return &MultiHandler{handlers: handlers}
}

// Enabled returns true if at least one of the handlers is enabled for the given level.
func (h *MultiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle passes the record to each handler.
func (h *MultiHandler) Handle(ctx context.Context, record slog.Record) error {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, record.Level) {
			_ = handler.Handle(ctx, record.Clone())
		}
	}
	return nil
}

// WithAttrs returns a new MultiHandler with the given attributes added to each child handler.
func (h *MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		handlers[i] = handler.WithAttrs(attrs)
	}
	return &MultiHandler{handlers: handlers}
}

// WithGroup returns a new MultiHandler with the given group name applied to each child handler.
func (h *MultiHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	handlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		handlers[i] = handler.WithGroup(name)
	}
	return &MultiHandler{handlers: handlers}
}
