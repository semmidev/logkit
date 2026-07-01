// Package logkit is an opinionated, extensible layer on top of the
// standard library's log/slog, built around three ideas:
//
//  1. Context propagation. Request/correlation/trace ids and ad-hoc
//     business fields travel on context.Context (see the logctx
//     sub-package) so any logkit.Logger call anywhere in the call graph
//     picks them up automatically -- no need to thread a *Logger through
//     every function signature.
//  2. Wide events / canonical log lines. Instead of many scattered,
//     low-context log statements per request, the event sub-package lets
//     you accumulate one rich event and emit it once (see
//     https://loggingsucks.com/ for the rationale).
//  3. Secure by construction. The secure sub-package gives you
//     SafeError (public/internal message separation) and Secret[T]
//     (type-level redaction), following the JetBrains guide to secure Go
//     error handling.
//
// Every extension point is a plain interface from the standard library
// or from this package: slog.Handler for output destinations,
// slog.LogValuer for redaction/custom formatting, sampler.Decider for
// sampling policy. Nothing here is closed for extension.
package logkit

import (
	"context"
	"io"
	"log/slog"
	"os"
	"runtime"
	"time"

	"github.com/semmidev/logkit/logctx"
	"github.com/semmidev/logkit/slogx"
)

// Logger wraps a slog.Handler with context-aware logging methods and a
// runtime-adjustable level. It is cheap to copy and safe for concurrent
// use (same guarantees as *slog.Logger, which it wraps).
type Logger struct {
	slog       *slog.Logger
	level      *slog.LevelVar
	addSource  bool
	writer     io.Writer
	callerSkip int
}

// RedactConfig configures advanced, path-based log redaction.
type RedactConfig = slogx.RedactConfig

// Format selects one of the built-in output formats for New. Use
// Config.Handler instead if you need something these don't cover --
// Format/Writer is a convenience, not the only way in.
type Format int

const (
	// FormatJSON writes newline-delimited JSON. Use this in production.
	FormatJSON Format = iota
	// FormatConsole writes colorized, human-readable lines. Use this for
	// local development.
	FormatConsole
)

// Config configures a Logger built with New. All fields are optional;
// the zero value produces a reasonable development-friendly logger
// (console format, info level, stderr).
type Config struct {
	// Format picks a built-in handler. Ignored if Handler is set.
	Format Format
	// Writer is where the built-in handler writes to. Defaults to
	// os.Stderr. Ignored if Handler is set.
	Writer io.Writer
	// Level sets the initial minimum level. Defaults to slog.LevelInfo.
	// You can change it later at runtime via Logger.SetLevel --
	// New always wraps this in an internal *slog.LevelVar regardless of
	// what's passed here.
	Level slog.Level
	// AddSource includes file:line on every record. Ignored if Handler
	// is set (configure it directly on your handler instead).
	AddSource bool
	// SensitiveKeys is a defense-in-depth redaction list forwarded to
	// the built-in handler (see slogx.HandlerOptions.SensitiveKeys).
	// Ignored if Handler is set.
	SensitiveKeys []string
	// Redact configures advanced, Pino-like redaction behavior. If set,
	// it takes precedence over SensitiveKeys. Ignored if Handler is set.
	Redact *RedactConfig

	// ServiceName, Version, Environment, if non-empty, are attached as
	// base attributes to every log line -- the minimum "which process
	// emitted this" context a wide event / canonical log line needs.
	ServiceName string
	Version     string
	Environment string

	// Handler, if set, is used as-is instead of building one from
	// Format/Writer/AddSource/SensitiveKeys. This is the escape hatch:
	// wrap your own slog.Handler (e.g. one that also ships to OTel, or a
	// third-party handler) and logkit's context-aware methods,
	// SafeError, Secret[T], and event/sampler machinery all keep
	// working on top of it unchanged.
	Handler slog.Handler
}

// New builds a Logger from cfg.
func New(cfg Config) *Logger {
	level := &slog.LevelVar{}
	level.Set(cfg.Level)

	w := cfg.Writer
	h := cfg.Handler
	if h == nil {
		if w == nil {
			w = os.Stderr
		}
		opts := slogx.HandlerOptions{
			Level:         level,
			AddSource:     cfg.AddSource,
			SensitiveKeys: cfg.SensitiveKeys,
			Redact:        cfg.Redact,
		}
		switch cfg.Format {
		case FormatConsole:
			h = slogx.NewConsoleHandler(w, opts)
		default:
			h = slogx.NewJSONHandler(w, opts)
		}
	}

	sl := slog.New(h)
	if cfg.ServiceName != "" {
		sl = sl.With("service", cfg.ServiceName)
	}
	if cfg.Version != "" {
		sl = sl.With("version", cfg.Version)
	}
	if cfg.Environment != "" {
		sl = sl.With("env", cfg.Environment)
	}

	addSource := cfg.AddSource
	if cfg.Handler != nil {
		addSource = true // Assume custom handler might need source
	}

	return &Logger{
		slog:       sl,
		level:      level,
		addSource:  addSource,
		writer:     w,
		callerSkip: 0,
	}
}

// NewDefault returns a ready-to-use development Logger: colorized
// console output on stderr, info level. Equivalent to
// New(Config{Format: FormatConsole}).
func NewDefault() *Logger {
	return New(Config{Format: FormatConsole})
}

// NewNop returns a Logger that discards everything. Useful as a default
// in code that takes a *Logger but shouldn't panic if the caller (e.g. a
// test) doesn't provide one.
func NewNop() *Logger {
	return New(Config{Handler: slog.NewJSONHandler(io.Discard, nil)})
}

// Slog returns the underlying *slog.Logger, for interoperability with
// libraries that expect one directly (e.g. http.Server.ErrorLog via
// slog.NewLogLogger, or third-party packages that accept a *slog.Logger
// for their own logging).
func (l *Logger) Slog() *slog.Logger { return l.slog }

// Leveler returns the *slog.LevelVar backing this logger, so callers can
// wire it up to, say, an admin HTTP endpoint or a SIGHUP handler to
// change verbosity without restarting the process.
func (l *Logger) Leveler() *slog.LevelVar { return l.level }

// SetLevel changes the minimum level for this logger (and everything
// derived from it via With/WithGroup, since they share the same
// *slog.LevelVar) at runtime.
func (l *Logger) SetLevel(level slog.Level) { l.level.Set(level) }

// With returns a new Logger with args attached to every subsequent log
// call, exactly like slog.Logger.With. Cheap; does not affect l.
func (l *Logger) With(args ...any) *Logger {
	return &Logger{slog: l.slog.With(args...), level: l.level, addSource: l.addSource, writer: l.writer, callerSkip: l.callerSkip}
}

// WithGroup returns a new Logger that nests subsequent attributes under
// name, exactly like slog.Logger.WithGroup.
func (l *Logger) WithGroup(name string) *Logger {
	return &Logger{slog: l.slog.WithGroup(name), level: l.level, addSource: l.addSource, writer: l.writer, callerSkip: l.callerSkip}
}

// WithCallerSkip returns a new Logger that skips an additional n frames
// when resolving the file:line of a log call. This is useful when writing
// helper functions or logging middleware wrappers.
func (l *Logger) WithCallerSkip(n int) *Logger {
	return &Logger{
		slog:       l.slog,
		level:      l.level,
		addSource:  l.addSource,
		writer:     l.writer,
		callerSkip: l.callerSkip + n,
	}
}

// contextAttrs pulls request/correlation/trace/span ids and any
// logctx.Enrich'd fields off ctx, returning them as a flat slog arg
// slice ready to prepend to a log call's own args. Missing ids are
// simply omitted (not logged as empty strings) to keep lines free of
// noise for code paths that don't have them (e.g. a background job with
// no request id but a job id enriched separately).
// log is the shared implementation behind every level method. It
// follows the pattern documented by log/slog itself for wrapping
// loggers (see the "Wrapping output methods" example in the slog
// package docs): a manual runtime.Callers + slog.NewRecord so that
// AddSource reports the call site in *user* code, not this file.
func (l *Logger) log(ctx context.Context, level slog.Level, msg string, args ...any) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !l.slog.Enabled(ctx, level) {
		return
	}
	var pc uintptr
	if l.addSource {
		var pcs [1]uintptr
		runtime.Callers(3+l.callerSkip, pcs[:]) // skip [Callers, this func, the exported level method, plus any callerSkip]
		pc = pcs[0]
	}
	r := slog.NewRecord(time.Now(), level, msg, pc)
	if ctx != nil {
		if v := logctx.RequestID(ctx); v != "" {
			r.AddAttrs(slog.String("request_id", v))
		}
		if v := logctx.CorrelationID(ctx); v != "" {
			r.AddAttrs(slog.String("correlation_id", v))
		}
		if v := logctx.TraceID(ctx); v != "" {
			r.AddAttrs(slog.String("trace_id", v))
		}
		if v := logctx.SpanID(ctx); v != "" {
			r.AddAttrs(slog.String("span_id", v))
		}
		logctx.AddFieldsToRecord(ctx, &r)
	}
	r.Add(args...)
	_ = l.slog.Handler().Handle(ctx, r)
}

// Sync flushes any buffered log entries. Call this before the program exits
// (for example, via defer logger.Sync()) to ensure all logs are written.
func (l *Logger) Sync() error {
	if l.writer == nil {
		return nil
	}
	if s, ok := l.writer.(interface{ Sync() error }); ok {
		return s.Sync()
	}
	if f, ok := l.writer.(interface{ Flush() error }); ok {
		return f.Flush()
	}
	return nil
}

// Trace logs at LevelTrace: the most verbose level, for detail too noisy
// even for Debug.
func (l *Logger) Trace(ctx context.Context, msg string, args ...any) {
	l.log(ctx, LevelTrace, msg, args...)
}

// Debug logs at slog.LevelDebug: development/diagnostic detail not
// normally enabled in production.
func (l *Logger) Debug(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelDebug, msg, args...)
}

// Info logs at slog.LevelInfo: normal operational messages and wide
// events / canonical log lines.
func (l *Logger) Info(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelInfo, msg, args...)
}

// Warn logs at slog.LevelWarn: recoverable problems worth a human's
// attention but not an immediate page.
func (l *Logger) Warn(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelWarn, msg, args...)
}

// Error logs at slog.LevelError. If args contains an error under the
// key "error" whose value implements slog.LogValuer (e.g.
// *secure.SafeError), it's expanded/redacted automatically -- no special
// handling needed at the call site beyond secure.Wrap/secure.New
// producing that kind of error in the first place.
func (l *Logger) Error(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelError, msg, args...)
}

// Fatal logs at LevelFatal and then terminates the process via
// os.Exit(1). Use sparingly and only at the outermost level (e.g.
// main(), or a supervisor goroutine deciding the process can't continue)
// -- never in library code, since it removes the caller's ability to
// handle the failure. Prefer returning an error everywhere else.
func (l *Logger) Fatal(ctx context.Context, msg string, args ...any) {
	l.log(ctx, LevelFatal, msg, args...)
	os.Exit(1)
}

// WithError is a small convenience for the extremely common
// `logger.Error(ctx, "...", "error", err)` call, saving four characters
// and a moment's thought about the key name so it stays consistent
// across a codebase: logger.WithError(err).Error(ctx, "operation failed").
func (l *Logger) WithError(err error) *Logger {
	return l.With("error", err)
}
