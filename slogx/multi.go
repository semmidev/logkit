package slogx

import (
	"context"
	"errors"
	"log/slog"
)

// multiHandler fans a single log record out to several handlers -- e.g.
// pretty console output for a developer's terminal AND newline-JSON to a
// file/collector at the same time, or JSON to stdout AND a separate
// "errors-only" handler that ships straight to an alerting sink.
type multiHandler struct {
	handlers []slog.Handler
}

// NewMultiHandler combines handlers into one. Enabled reports true if
// ANY child handler is enabled for that level (so a record reaches every
// handler that wants it); each enabled child receives the record
// independently, and Handle collects (rather than short-circuits on) any
// per-handler errors.
func NewMultiHandler(handlers ...slog.Handler) slog.Handler {
	return &multiHandler{handlers: handlers}
}

func (m *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range m.handlers {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		// Each handler gets its own clone of the record since
		// slog.Record's Attrs iterator is single-use.
		if err := h.Handle(ctx, r.Clone()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		next[i] = h.WithAttrs(attrs)
	}
	return &multiHandler{handlers: next}
}

func (m *multiHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		next[i] = h.WithGroup(name)
	}
	return &multiHandler{handlers: next}
}
