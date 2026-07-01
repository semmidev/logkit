// Package logctx carries per-request identifiers (request id, correlation
// id, trace/span id, tenant/user id, ...) through context.Context so that
// any logger call deep in the stack can pick them up automatically,
// without every function signature needing to grow a *Logger parameter.
//
// This is the piece that makes logkit's "context propagation from
// middleware" story work: a middleware calls logctx.WithRequestID (and
// friends) once at the edge of the request, and every logkit.Logger call
// downstream that receives that context will attach the same ids.
package logctx

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"sync"
)

// ctxKey is an unexported type so keys from this package can never
// collide with keys set by other packages using context.WithValue.
type ctxKey int

const (
	keyRequestID ctxKey = iota
	keyCorrelationID
	keyTraceID
	keySpanID
	keyFields
)

// NewID generates a new random identifier suitable for a request or
// correlation id (a UUIDv4, RFC 4122). It deliberately has zero external
// dependencies -- built on crypto/rand only -- so that swapping this
// implementation (e.g. for ULIDs, which sort lexicographically by time)
// is a one-function change, and this library doesn't drag in a uuid
// package as a transitive dependency.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is essentially unrecoverable (no entropy
		// source); fall back to a degraded-but-non-panicking value rather
		// than crash a request path just because ID generation failed.
		return "00000000-0000-4000-8000-000000000000"
	}
	// Set version (4) and variant (RFC 4122) bits per spec.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// WithRequestID returns a copy of ctx carrying id as the request id.
// The request id identifies a single inbound request to *this* service.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyRequestID, id)
}

// RequestID returns the request id stored in ctx, or "" if none is set.
func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(keyRequestID).(string)
	return v
}

// WithCorrelationID returns a copy of ctx carrying id as the correlation
// id. Unlike the request id, the correlation id is expected to survive
// across service boundaries (propagated via an HTTP header or gRPC
// metadata) so that a single logical operation can be traced across
// many services even without full distributed tracing.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyCorrelationID, id)
}

// CorrelationID returns the correlation id stored in ctx, or "" if none
// is set.
func CorrelationID(ctx context.Context) string {
	v, _ := ctx.Value(keyCorrelationID).(string)
	return v
}

// WithTraceID / WithSpanID let logkit interoperate with a tracing system
// (OpenTelemetry or otherwise) without taking a hard dependency on one.
// If you're using otel, wire your tracer's span context into these
// (or, better, use slogx.OtelExtractor, see slogx/otel.go) so trace_id
// and span_id show up on every log line for that request.
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyTraceID, id)
}

func TraceID(ctx context.Context) string {
	v, _ := ctx.Value(keyTraceID).(string)
	return v
}

func WithSpanID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keySpanID, id)
}

func SpanID(ctx context.Context) string {
	v, _ := ctx.Value(keySpanID).(string)
	return v
}

// Fields is a small, ordered bag of arbitrary key/value pairs that can be
// attached to a context and later merged automatically into every log
// line written with that context (see logkit.Logger.log). It's the
// mechanism behind logkit.Enrich / EnrichFrom, which callers use to add
// business context (user id, tenant, feature flags, ...) to a request's
// context deep inside a handler, without needing a reference to the
// *Logger itself.
type Fields struct {
	mu sync.Mutex
	kv []any // flat key, value, key, value... matching slog.Logger.Log
}

// WithFields returns a copy of ctx with an attached, empty Fields bag.
// Call this once near the top of a request (usually inside your logging
// middleware); Enrich then appends to it deeper in the stack, possibly
// from multiple goroutines fanned out to serve the same request -- the
// bag is safe for concurrent use.
func WithFields(ctx context.Context) context.Context {
	return context.WithValue(ctx, keyFields, &Fields{})
}

// Enrich appends args (in slog's alternating key/value form, or using
// slog.Attr values) to the Fields bag stored in ctx, if any. If ctx has
// no Fields bag (WithFields was never called on an ancestor context),
// Enrich is a documented no-op -- it never panics -- so it's always safe
// to call speculatively from library code that doesn't know whether
// logkit is wired up.
func Enrich(ctx context.Context, args ...any) {
	f, ok := ctx.Value(keyFields).(*Fields)
	if !ok || f == nil {
		return
	}
	f.mu.Lock()
	f.kv = append(f.kv, args...)
	f.mu.Unlock()
}

// FieldsFromContext returns a snapshot of the flat key/value slice
// accumulated via Enrich, or nil if ctx carries no Fields bag.
func FieldsFromContext(ctx context.Context) []any {
	f, ok := ctx.Value(keyFields).(*Fields)
	if !ok || f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]any, len(f.kv))
	copy(out, f.kv)
	return out
}

// AddFieldsToRecord appends every key/value pair stored in ctx's Fields bag
// directly to the given slog.Record. It performs this under a mutex lock to
// prevent race conditions, avoiding intermediate copy/slice allocations.
func AddFieldsToRecord(ctx context.Context, r *slog.Record) {
	f, ok := ctx.Value(keyFields).(*Fields)
	if !ok || f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.kv) > 0 {
		r.Add(f.kv...)
	}
}
