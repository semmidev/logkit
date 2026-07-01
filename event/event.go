// Package event implements the "wide event" / "canonical log line"
// pattern from https://loggingsucks.com/: instead of scattering 15
// vague log statements across a request's lifecycle, accumulate a
// single, rich, structured event as the request flows through your
// code, then emit it exactly once when the request finishes.
//
// The typical flow:
//
//  1. Middleware creates an Event at the start of a request and stores
//     it on the context (see WithEvent).
//  2. Handlers and lower-level code pull it back out (FromContext) and
//     call Set/SetError to attach whatever context they know about --
//     the user, the cart, the payment attempt, the error -- without any
//     of them needing to know about each other or about logging
//     infrastructure.
//  3. The same middleware emits the event once, at the end, through a
//     Logger and (optionally) a sampler.Decider.
package event

import (
	"context"
	"sync"
	"time"
)

type ctxKey struct{}

// Event accumulates structured fields for a single logical operation
// (usually one inbound request) so they can be emitted as ONE log line
// instead of many. Safe for concurrent use: handlers fanning work out
// across goroutines for the same request can all call Set/SetError on
// the same Event.
type Event struct {
	mu        sync.Mutex
	fields    map[string]any
	startedAt time.Time
}

// New creates an empty Event with its start time recorded, for computing
// duration_ms automatically at Snapshot/Fields time.
func New() *Event {
	return &Event{
		fields:    make(map[string]any, 32),
		startedAt: time.Now(),
	}
}

// Set attaches key=val to the event. Later calls with the same key
// overwrite earlier ones (last write wins), which is usually what you
// want: e.g. a retried payment attempt should show its final state, not
// its first.
//
// Prefer flat or lightly-nested values (numbers, strings, bools, small
// maps) -- the whole point of a wide event is that it's still one
// queryable line, not a deeply nested blob.
func (e *Event) Set(key string, val any) *Event {
	e.mu.Lock()
	e.fields[key] = val
	e.mu.Unlock()
	return e
}

// SetMap merges every key in m into the event, same overwrite semantics
// as Set. Handy for attaching a whole "user context" or "cart context"
// struct-as-map in one call.
func (e *Event) SetMap(m map[string]any) *Event {
	e.mu.Lock()
	for k, v := range m {
		e.fields[k] = v
	}
	e.mu.Unlock()
	return e
}

// SetError records err on the event under "error" (its message) and
// "error_type" (its Go type name via a lightweight type switch), and
// keeps the original error value under "error_detail" so that if the
// error implements slog.LogValuer (e.g. *secure.SafeError) the eventual
// slog call still redacts/expands it correctly instead of just getting a
// flattened string.
//
// If err is nil, SetError clears any previously recorded error and marks
// the event as a non-error outcome.
func (e *Event) SetError(err error) *Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err == nil {
		delete(e.fields, "error")
		delete(e.fields, "error_detail")
		e.fields["outcome"] = "success"
		return e
	}
	e.fields["error"] = err.Error()
	e.fields["error_detail"] = err // may be a slog.LogValuer; resolved at emit time
	e.fields["outcome"] = "error"
	return e
}

// Snapshot returns a copy of the event's fields plus a computed
// "duration_ms" (time since New()), suitable for handing to a
// sampler.Decider or for tests. Mutating the returned map does not
// affect the Event.
func (e *Event) Snapshot() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]any, len(e.fields)+1)
	for k, v := range e.fields {
		if k == "error_detail" {
			continue // internal-only, not meant for the sampler/snapshot view
		}
		out[k] = v
	}
	out["duration_ms"] = float64(time.Since(e.startedAt).Microseconds()) / 1000.0
	return out
}

// Args returns the event's fields as a flat key/value slice in slog's
// alternating form, ready to pass straight to a Logger call:
//
//	logger.Info(ctx, "request completed", ev.Args()...)
//
// error_detail (if set) is emitted under the key "error" as the raw
// error value (not a2 string), so a *secure.SafeError attached via
// SetError still goes through its LogValue() redaction/expansion at
// the point slog actually formats the record.
func (e *Event) Args() []any {
	e.mu.Lock()
	defer e.mu.Unlock()

	args := make([]any, 0, len(e.fields)*2+2)
	for k, v := range e.fields {
		if k == "error" && e.fields["error_detail"] != nil {
			continue // superseded by the raw error_detail below
		}
		if k == "error_detail" {
			continue
		}
		args = append(args, k, v)
	}
	if ed, ok := e.fields["error_detail"]; ok {
		args = append(args, "error", ed)
	}
	args = append(args, "duration_ms", float64(time.Since(e.startedAt).Microseconds())/1000.0)
	return args
}

// WithEvent returns a copy of ctx carrying ev, retrievable via
// FromContext.
func WithEvent(ctx context.Context, ev *Event) context.Context {
	return context.WithValue(ctx, ctxKey{}, ev)
}

// FromContext returns the Event stored in ctx, if any.
func FromContext(ctx context.Context) (*Event, bool) {
	ev, ok := ctx.Value(ctxKey{}).(*Event)
	return ev, ok
}

// Enrich is a convenience for the common "get the event from context and
// set a field on it, no-op if there isn't one" pattern, mirroring
// logctx.Enrich:
//
//	event.Enrich(ctx, "user.tier", "premium")
func Enrich(ctx context.Context, key string, val any) {
	if ev, ok := FromContext(ctx); ok {
		ev.Set(key, val)
	}
}
