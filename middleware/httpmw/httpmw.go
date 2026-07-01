// Package httpmw wires logkit into an HTTP server: it generates/propagates
// a request id, builds a wide event (see the event package) for the
// request, recovers panics into a safe 500 response, and emits exactly
// one canonical log line per request when it completes.
//
// Because it's just a func(http.Handler) http.Handler, it plugs directly
// into net/http, chi, gorilla/mux, or anything else using the standard
// middleware signature -- no adapter needed.
package httpmw

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/semmidev/logkit"
	"github.com/semmidev/logkit/event"
	"github.com/semmidev/logkit/logctx"
	"github.com/semmidev/logkit/sampler"
	"github.com/semmidev/logkit/secure"
)

// Config controls Middleware's behavior. The zero value is usable:
// generates request ids, uses the X-Request-Id header if present, logs
// every request via the given Logger.
type Config struct {
	// Logger is required: the canonical log line is emitted through it.
	Logger *logkit.Logger

	// RequestIDHeader is the inbound/outbound header used to
	// propagate the request id. Defaults to "X-Request-Id".
	RequestIDHeader string

	// CorrelationIDHeader is the inbound/outbound header used to
	// propagate the correlation id across service boundaries. Defaults
	// to "X-Correlation-Id". If absent on an inbound request, the
	// request id is reused as the correlation id (so a single-service
	// call still has one).
	CorrelationIDHeader string

	// Sampler decides which completed request events actually get
	// logged. Defaults to sampler.Always (log everything) -- opt into
	// sampler.NewTailSampler(...) once volume warrants it.
	Sampler sampler.Decider

	// GenerateID overrides how new request/correlation ids are minted.
	// Defaults to logctx.NewID (random UUIDv4).
	GenerateID func() string
}

// Middleware returns HTTP middleware built from cfg.
func Middleware(cfg Config) func(http.Handler) http.Handler {
	if cfg.RequestIDHeader == "" {
		cfg.RequestIDHeader = "X-Request-Id"
	}
	if cfg.CorrelationIDHeader == "" {
		cfg.CorrelationIDHeader = "X-Correlation-Id"
	}
	if cfg.Sampler == nil {
		cfg.Sampler = sampler.Always
	}
	if cfg.GenerateID == nil {
		cfg.GenerateID = logctx.NewID
	}
	logger := cfg.Logger
	if logger == nil {
		logger = logkit.NewNop()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			reqID := r.Header.Get(cfg.RequestIDHeader)
			if reqID == "" {
				reqID = cfg.GenerateID()
			}
			corrID := r.Header.Get(cfg.CorrelationIDHeader)
			if corrID == "" {
				corrID = reqID
			}

			ctx := r.Context()
			ctx = logctx.WithRequestID(ctx, reqID)
			ctx = logctx.WithCorrelationID(ctx, corrID)
			ctx = logctx.WithFields(ctx)

			ev := event.New()
			ev.Set("http.method", r.Method)
			ev.Set("http.path", r.URL.Path)
			ev.Set("http.remote_addr", r.RemoteAddr)
			if ua := r.UserAgent(); ua != "" {
				ev.Set("http.user_agent", ua)
			}
			ctx = event.WithEvent(ctx, ev)

			w.Header().Set(cfg.RequestIDHeader, reqID)
			w.Header().Set(cfg.CorrelationIDHeader, corrID)

			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			req := r.WithContext(ctx)

			defer func() {
				if rec := recover(); rec != nil {
					// Fail secure: a panic means the process may be in an
					// inconsistent state for *this request*. Recover just
					// enough to return a generic 500 and log the truth --
					// never leak the panic value (which could be, or
					// contain, anything) to the client.
					err := panicToError(rec)
					ev.SetError(secure.Wrap(secure.CodeInternal, "An internal error occurred.", err))
					ev.Set("http.panicked", true)
					if !sw.wroteHeader {
						http.Error(sw, "Internal Server Error", http.StatusInternalServerError)
					}
				}

				ev.Set("http.status_code", sw.status)
				emit(req.Context(), logger, cfg.Sampler, ev, start)
			}()

			next.ServeHTTP(sw, req)
		})
	}
}

// emit finalizes and logs the wide event for a request, honoring the
// configured sampler. Errors always bypass sampling (see
// sampler.RuleHasError, included by default in NewTailSampler, but we
// also hard-guarantee it here since dropping an error line is worse than
// dropping a happy-path line even if someone configures a custom
// sampler.Decider that forgot to keep errors).
func emit(ctx context.Context, logger *logkit.Logger, s sampler.Decider, ev *event.Event, start time.Time) {
	snap := ev.Snapshot()
	hasError := snap["error"] != nil
	status, _ := snap["http.status_code"].(int)

	if !hasError && status < 500 && !s.ShouldSample(snap) {
		return
	}

	level := "info"
	if hasError || status >= 500 {
		level = "error"
	} else if status >= 400 {
		level = "warn"
	}

	msg := "http request completed"
	switch level {
	case "error":
		logger.Error(ctx, msg, ev.Args()...)
	case "warn":
		logger.Warn(ctx, msg, ev.Args()...)
	default:
		logger.Info(ctx, msg, ev.Args()...)
	}
}

func panicToError(rec any) error {
	if err, ok := rec.(error); ok {
		return err
	}
	return errString(fmt.Sprintf("%v", rec))
}

type errString string

func (e errString) Error() string { return string(e) }

// statusWriter captures the status code written so it can be attached to
// the wide event, since http.ResponseWriter doesn't expose it directly.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}
