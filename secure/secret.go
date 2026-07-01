// Package secure provides the building blocks recommended by secure Go
// error-handling guidance (see the JetBrains "Secure Error Handling in
// Go" guide) on top of log/slog:
//
//   - Secret[T]: a generic wrapper that redacts a value everywhere it is
//     logged, printed, or JSON-marshaled -- fixing the mistake at the
//     type level instead of relying on every call site remembering to
//     redact.
//   - SafeError: a "split brain" error type that separates the safe,
//     public-facing message from the unsafe internal cause, so that
//     accidentally writing err.Error() to an HTTP response can't leak
//     internals.
//   - Redactor / RedactFunc: hooks for sanitizing arbitrary structs
//     (e.g. an inbound request DTO) before they're attached to a log
//     line.
package secure

import (
	"encoding/json"
	"log/slog"
)

// redactedPlaceholder is what a Secret renders as everywhere: logs,
// fmt.Stringer, and JSON. Kept short and unambiguous on purpose.
const redactedPlaceholder = "***REDACTED***"

// Secret wraps a value of any type so that it can be passed around and
// used normally in code, but can never accidentally end up in a log
// line, error message, or JSON payload in cleartext.
//
// Wrap sensitive config and request fields at the type level:
//
//	type Config struct {
//	    DBPassword secure.Secret[string]
//	    APIKey     secure.Secret[string]
//	}
//
//	logger.Info(ctx, "config loaded", "config", cfg) // password/key print as ***REDACTED***
//
// Use Reveal() only at the point where the raw value is actually needed
// (e.g. handing a password to a DB driver) -- never store the revealed
// value anywhere that might get logged.
type Secret[T any] struct {
	value T
}

// NewSecret wraps v so it redacts automatically wherever it's logged or
// serialized.
func NewSecret[T any](v T) Secret[T] {
	return Secret[T]{value: v}
}

// Reveal returns the underlying value. Callers should use this as late
// as possible and never pass the result to a logger, error message, or
// anything else that might persist or transmit it.
func (s Secret[T]) Reveal() T {
	return s.value
}

// String implements fmt.Stringer so that %v, %s, and plain string
// concatenation all redact by default too -- not just structured
// logging.
func (s Secret[T]) String() string {
	return redactedPlaceholder
}

// LogValue implements slog.LogValuer. This is the hook slog itself calls
// before emitting any attribute, so a Secret[T] placed anywhere in a log
// call -- top-level, nested in a struct via slog.Any, inside a group --
// is redacted automatically without the caller having to remember to
// scrub it.
func (s Secret[T]) LogValue() slog.Value {
	return slog.StringValue(redactedPlaceholder)
}

// MarshalJSON ensures the redaction also holds if a struct containing a
// Secret is JSON-encoded directly (e.g. accidentally echoed back in an
// API response, or logged via a JSON-dumping debug handler that doesn't
// go through slog).
func (s Secret[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(redactedPlaceholder)
}

// Redactor is implemented by types that know how to produce a
// log-safe/serialize-safe copy of themselves. Prefer Secret[T] for
// individual fields; implement Redactor on a whole struct (typically a
// request/response DTO) when several fields need scrubbing together or
// the redaction is conditional.
//
//	func (r LoginRequest) Redact() any {
//	    return struct {
//	        Username string `json:"username"`
//	        Password string `json:"password"`
//	    }{r.Username, "***REDACTED***"}
//	}
//
//	logger.Info(ctx, "login attempt", "req", secure.Redact(req))
type Redactor interface {
	Redact() any
}

// redactedWrapper adapts the result of Redactor.Redact (or a fallback
// value) to slog.LogValuer so it round-trips through slog cleanly.
type redactedWrapper struct{ v any }

func (w redactedWrapper) LogValue() slog.Value {
	return slog.AnyValue(w.v)
}

// Redact returns a value safe to pass to a logger: if v implements
// Redactor, its Redact() output is used; otherwise v is returned
// unchanged. This makes call sites uniform regardless of whether the
// argument happens to implement Redactor:
//
//	logger.Info(ctx, "request", "body", secure.Redact(body))
func Redact(v any) any {
	if r, ok := v.(Redactor); ok {
		return redactedWrapper{r.Redact()}
	}
	return v
}
