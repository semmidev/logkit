package secure

import (
	"errors"
	"fmt"
	"log/slog"
)

// SafeError enforces, at the type level, the separation between what the
// *system* is allowed to know about a failure and what the *caller*
// (a user, or another service across a trust boundary) is allowed to
// see. This is the "split brain" pattern: a struct with an Internal
// (unsafe) and a UserMsg (safe) side, so a developer who forgets and
// writes `http.Error(w, err.Error(), 500)` leaks nothing, because
// Error() always returns the safe side.
type SafeError struct {
	// Code is a stable, machine-readable identifier ("AUTH_FAILED",
	// "RESOURCE_NOT_FOUND", ...) that clients can safely branch on.
	Code string

	// UserMsg is a human-readable message safe for external consumption:
	// generic, non-specific, no identifiers, no internals.
	UserMsg string

	// Internal is the raw upstream error. It is intentionally NOT
	// exposed via Unwrap() by default (see Wrap/WrapOpaque below), so a
	// vulnerability or verbose message in a third-party library can't be
	// introspected by a caller manipulating inputs to probe error types.
	// It IS available to your own logging/observability code via
	// LogValue(), Cause(), or LogString().
	Internal error

	// Metadata is structured, already-sanitized context for logs/traces
	// (request id, attempted username, upstream service name, ...).
	// Never put raw, unsanitized user input here -- see New's doc
	// comment.
	Metadata map[string]string

	// unwrap, when true, makes Unwrap() return Internal so
	// errors.Is/errors.As can traverse into the cause. Off by default;
	// enabled via WrapTransparent for the "crossing subsystem
	// boundaries" case where the caller is trusted internal code that
	// legitimately needs to inspect sentinel errors (e.g.
	// sql.ErrNoRows).
	unwrap bool
}

// New builds a SafeError. Prefer this (or a small domain-specific
// wrapper around it, e.g. NewAuthFailed(...)) over constructing
// SafeError{} literals directly at call sites -- funneling every error
// through a constructor is what forces someone to consciously choose
// what goes into Metadata instead of defaulting to "log the whole
// request struct" and leaking a password or token.
func New(code, userMsg string, internal error, metadata map[string]string) *SafeError {
	return &SafeError{
		Code:     code,
		UserMsg:  userMsg,
		Internal: internal,
		Metadata: metadata,
	}
}

// Wrap is an alias for New with no metadata, for the common case of
// opaque wrapping at a subsystem/service boundary:
//
//	user, err := db.QueryUser(id)
//	if err != nil {
//	    return nil, secure.Wrap("FETCH_ERROR", "Unable to retrieve user profile.", err)
//	}
//
// The result does NOT expose Internal via Unwrap(), so
// errors.Is(err, sql.ErrNoRows) fails on purpose above this boundary --
// translate specific causes to specific Codes/UserMsgs *before* wrapping
// if the caller needs to branch on them.
func Wrap(code, userMsg string, internal error) *SafeError {
	return New(code, userMsg, internal, nil)
}

// WrapTransparent behaves like Wrap but preserves the Unwrap() chain, so
// errors.Is / errors.As / errors.AsType can see through to Internal.
// Use this only when the caller is trusted, internal code within the
// same trust boundary -- never on a value that will cross an API or
// public boundary, since that would defeat the containment SafeError
// exists to provide.
func WrapTransparent(code, userMsg string, internal error) *SafeError {
	return &SafeError{Code: code, UserMsg: userMsg, Internal: internal, unwrap: true}
}

// Error implements the standard error interface. CRITICAL: this returns
// only the safe, public message -- never Internal -- specifically so
// that accidentally printing the error (fmt.Println(err), err.Error(),
// %v, an unguarded http.Error(w, err.Error(), 500), ...) can't leak
// internals. Get the internal detail explicitly via Cause() or
// LogString() when you actually mean to log it.
func (e *SafeError) Error() string {
	if e.UserMsg != "" {
		return e.UserMsg
	}
	return "internal error"
}

// Unwrap only exposes the internal cause when the error was built with
// WrapTransparent. This is what makes SafeError act as a firewall by
// default: errors.Is / errors.As stop here unless a caller explicitly
// opted in.
func (e *SafeError) Unwrap() error {
	if e.unwrap {
		return e.Internal
	}
	return nil
}

// Cause returns the raw internal error regardless of the unwrap setting.
// Unlike Unwrap, this is meant to be called deliberately by
// logging/observability code -- not by generic errors.Is/As traversal --
// so it's always available even for opaque errors.
func (e *SafeError) Cause() error {
	return e.Internal
}

// LogString returns a detailed, single-line representation intended for
// your own logs (SRE/on-call), including the code, safe message,
// internal cause, and metadata. Never send this to an external caller.
func (e *SafeError) LogString() string {
	return fmt.Sprintf("code=%s msg=%q cause=%v meta=%v", e.Code, e.UserMsg, e.Internal, e.Metadata)
}

// LogValue implements slog.LogValuer so that passing a *SafeError
// directly to a logkit/slog call (e.g. logger.Error(ctx, "failed",
// "error", err)) automatically emits a structured group with the code,
// safe message, internal cause, and metadata -- the "log the truth,
// return the safe message" split happens for free at every call site
// that uses this, instead of relying on each one to call LogString()
// correctly.
func (e *SafeError) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.String("code", e.Code),
		slog.String("message", e.UserMsg),
	}
	if e.Internal != nil {
		attrs = append(attrs, slog.String("cause", e.Internal.Error()))
	}
	if len(e.Metadata) > 0 {
		metaAttrs := make([]any, 0, len(e.Metadata)*2)
		for k, v := range e.Metadata {
			metaAttrs = append(metaAttrs, k, v)
		}
		attrs = append(attrs, slog.Group("metadata", metaAttrs...))
	}
	return slog.GroupValue(attrs...)
}

// As reports whether err is (or wraps, transparently) a *SafeError, and
// if so returns it. Thin convenience over errors.As.
func As(err error) (*SafeError, bool) {
	var se *SafeError
	ok := errors.As(err, &se)
	return se, ok
}

// Is reports whether err is a *SafeError with the given Code. Useful at
// API boundaries to translate specific SafeErrors into specific HTTP
// statuses/gRPC codes without needing sentinel error values per case.
func Is(err error, code string) bool {
	se, ok := As(err)
	return ok && se.Code == code
}

// Generic, ready-to-use codes for the most common cases. Domain code is
// expected to define its own additional codes alongside these; keep the
// set small and stable since Code is part of your public API contract
// once external clients start branching on it.
const (
	CodeInternal     = "INTERNAL"
	CodeNotFound     = "NOT_FOUND"
	CodeInvalidInput = "INVALID_INPUT"
	CodeUnauthorized = "UNAUTHORIZED"
	CodeForbidden    = "FORBIDDEN"
	CodeConflict     = "CONFLICT"
	CodeUnavailable  = "UNAVAILABLE"
	CodeTimeout      = "TIMEOUT"
)

// Internal wraps any error as a generic, maximally-safe SafeError. This
// is the required catch-all at a public boundary: if you don't
// recognize the error type, ASSUME it's sensitive and don't let any of
// its detail through. See translate examples in middleware/httpmw.
func Internal(err error) *SafeError {
	return Wrap(CodeInternal, "An internal error occurred. Please contact support.", err)
}
