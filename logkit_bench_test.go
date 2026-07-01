package logkit_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/semmidev/logkit"
	"github.com/semmidev/logkit/logctx"
	"github.com/semmidev/logkit/slogx"
)

func BenchmarkLoggerInfo_NoContext(b *testing.B) {
	logger := logkit.New(logkit.Config{
		Format: logkit.FormatJSON,
		Writer: io.Discard,
		Level:  slog.LevelInfo,
	})
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Info(ctx, "hello", "key", "value")
	}
}

func BenchmarkLoggerInfo_WithContextID(b *testing.B) {
	logger := logkit.New(logkit.Config{
		Format: logkit.FormatJSON,
		Writer: io.Discard,
		Level:  slog.LevelInfo,
	})
	ctx := context.Background()
	ctx = logctx.WithRequestID(ctx, "req-123")
	ctx = logctx.WithCorrelationID(ctx, "corr-456")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Info(ctx, "hello", "key", "value")
	}
}

func BenchmarkLoggerInfo_WithContextFields(b *testing.B) {
	logger := logkit.New(logkit.Config{
		Format: logkit.FormatJSON,
		Writer: io.Discard,
		Level:  slog.LevelInfo,
	})
	ctx := context.Background()
	ctx = logctx.WithFields(ctx)
	logctx.Enrich(ctx, "user_id", "u-789", "role", "admin")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Info(ctx, "hello", "key", "value")
	}
}

func BenchmarkLoggerInfo_AsyncWriter(b *testing.B) {
	aw := slogx.NewAsyncWriter(io.Discard, 64*1024, 100*time.Millisecond)
	defer aw.Close()

	logger := logkit.New(logkit.Config{
		Format: logkit.FormatJSON,
		Writer: aw,
		Level:  slog.LevelInfo,
	})
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Info(ctx, "hello", "key", "value")
	}
}
