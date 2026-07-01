// Example: basic usage of logkit outside of any HTTP/gRPC framework --
// levels, context propagation, secret redaction, and safe errors.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/semmidev/logkit"
	"github.com/semmidev/logkit/logctx"
	"github.com/semmidev/logkit/secure"
	"github.com/semmidev/logkit/slogx"
)

type Config struct {
	DBHost     string
	DBPassword secure.Secret[string]
}

func main() {
	// Create an asynchronous buffered writer that flushes logs in the background,
	// drastically improving performance in production environments (like Pino's async mode).
	asyncWriter := slogx.NewAsyncWriter(os.Stderr, 8192, 100*time.Millisecond)
	// Ensure all buffered logs are flushed before exiting.
	defer asyncWriter.Close()

	logger := logkit.New(logkit.Config{
		Format:      logkit.FormatConsole, // switch to logkit.FormatJSON in production
		Writer:      asyncWriter,          // Pipe through our high-performance async writer
		Level:       slog.LevelDebug,
		ServiceName: "billing-service",
		Version:     "1.4.2",
		Environment: "development",
		AddSource:   true,
		// Advanced Pino-like path redaction configurations:
		Redact: &logkit.RedactConfig{
			Paths: []string{
				"password",                  // match key case-insensitively anywhere
				"req.headers.authorization", // match specific nested path
				"secrets.tokens[*].key",     // match wildcards in lists
			},
			Censor: "[GDPR REDACTED]",
		},
	})
	defer logger.Sync()

	ctx := context.Background()
	// Simulate what a middleware would do: stamp the context with a
	// request id and a correlation id for this "unit of work".
	ctx = logctx.WithRequestID(ctx, logctx.NewID())
	ctx = logctx.WithCorrelationID(ctx, logctx.NewID())

	logger.Info(ctx, "service starting", "port", 8080)
	logger.Debug(ctx, "loaded config", "config", loadConfig())
	logger.Trace(ctx, "very verbose detail", "raw_bytes", 128)

	// Showcase advanced path-based and wildcard redaction matching Pino behavior:
	logger.Info(ctx, "inbound request headers",
		slog.Group("req",
			slog.Group("headers",
				slog.String("authorization", "bearer secret-inbound-token"),
				slog.String("user-agent", "Mozilla/5.0"),
			),
		),
	)

	logger.Info(ctx, "processing secrets group",
		slog.Group("secrets",
			slog.Group("tokens",
				slog.String("key", "secret-api-key"),
				slog.String("name", "billing-token"),
			),
		),
	)

	if err := doWork(ctx, logger); err != nil {
		logger.Error(ctx, "work failed", "error", err)
	}

	// With() creates a child logger that always includes these fields --
	// handy for a worker/job loop where every line should carry the job id.
	jobLogger := logger.With("job_id", "job_9x8w7v6u", "job_type", "invoice_email")
	jobLogger.Info(ctx, "job started")
	jobLogger.Info(ctx, "job finished", "emails_sent", 42)
}

func loadConfig() Config {
	return Config{
		DBHost:     "db.internal:5432",
		DBPassword: secure.NewSecret("s3cr3t-should-never-appear-in-logs"),
	}
}

// doWork demonstrates the secure error handling pattern: the DB layer
// returns a raw, detail-leaking error; the service layer contains it
// behind a SafeError before it goes anywhere near a log line or a
// caller.
func doWork(ctx context.Context, logger *logkit.Logger) error {
	if err := queryDatabase(); err != nil {
		// Opaque wrap at the subsystem boundary: callers of doWork can't
		// errors.Is/As into the raw pq error, only into the SafeError's Code.
		return secure.Wrap(secure.CodeUnavailable, "The invoicing database is temporarily unavailable.", err)
	}
	return nil
}

func queryDatabase() error {
	// Pretend this is what a real driver returned -- full of internal
	// detail (host, port, schema) that must never reach a client.
	return errors.New(`pq: connection to "db.internal:5432/billing" refused`)
}
