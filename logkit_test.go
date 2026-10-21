package logkit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/semmidev/logkit"
	"github.com/semmidev/logkit/logctx"
	"github.com/semmidev/logkit/secure"
)

func newTestLogger(buf *bytes.Buffer, level slog.Level) *logkit.Logger {
	return logkit.New(logkit.Config{
		Format: logkit.FormatJSON,
		Writer: buf,
		Level:  level,
	})
}

func decodeLastLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var out map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &out); err != nil {
		t.Fatalf("failed to decode log line %q: %v", lines[len(lines)-1], err)
	}
	return out
}

func TestContextPropagation(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, slog.LevelInfo)

	ctx := context.Background()
	ctx = logctx.WithRequestID(ctx, "req-123")
	ctx = logctx.WithCorrelationID(ctx, "corr-456")
	ctx = logctx.WithFields(ctx)
	logctx.Enrich(ctx, "user_id", "u-789")

	logger.Info(ctx, "hello")

	line := decodeLastLine(t, &buf)
	if line["request_id"] != "req-123" {
		t.Errorf("request_id = %v, want req-123", line["request_id"])
	}
	if line["correlation_id"] != "corr-456" {
		t.Errorf("correlation_id = %v, want corr-456", line["correlation_id"])
	}
	if line["user_id"] != "u-789" {
		t.Errorf("user_id = %v, want u-789 (from logctx.Enrich)", line["user_id"])
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, slog.LevelInfo)
	ctx := context.Background()

	logger.Debug(ctx, "should not appear")
	logger.Trace(ctx, "should not appear either")
	if buf.Len() != 0 {
		t.Fatalf("expected no output below configured level, got: %s", buf.String())
	}

	logger.Info(ctx, "should appear")
	if buf.Len() == 0 {
		t.Fatal("expected output at configured level")
	}
}

func TestRuntimeLevelChange(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, slog.LevelInfo)
	ctx := context.Background()

	logger.Debug(ctx, "invisible at info")
	if buf.Len() != 0 {
		t.Fatalf("expected debug to be filtered at info level")
	}

	logger.SetLevel(slog.LevelDebug)
	logger.Debug(ctx, "now visible at debug")
	if buf.Len() == 0 {
		t.Fatal("expected debug output after SetLevel(Debug)")
	}
}

func TestSecretRedaction(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, slog.LevelInfo)
	ctx := context.Background()

	secret := secure.NewSecret("super-secret-password")
	logger.Info(ctx, "config loaded", "password", secret)

	if strings.Contains(buf.String(), "super-secret-password") {
		t.Fatalf("secret leaked into log output: %s", buf.String())
	}
	line := decodeLastLine(t, &buf)
	if line["password"] != "***REDACTED***" {
		t.Errorf("password = %v, want ***REDACTED***", line["password"])
	}
}

func TestSensitiveKeyDefenseInDepth(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := logkit.New(logkit.Config{
		Format:        logkit.FormatJSON,
		Writer:        buf,
		Level:         slog.LevelInfo,
		SensitiveKeys: []string{"token"},
	})
	ctx := context.Background()

	// Even a raw, non-Secret string under a sensitive key name gets
	// caught by the belt-and-suspenders ReplaceAttr-based redaction.
	logger.Info(ctx, "auth ok", "token", "raw-plaintext-token")

	if strings.Contains(buf.String(), "raw-plaintext-token") {
		t.Fatalf("sensitive key leaked into log output: %s", buf.String())
	}
}

func TestSafeErrorDoesNotLeakInternal(t *testing.T) {
	internal := errorString("pq: connection refused to db.internal:5432")
	err := secure.Wrap(secure.CodeUnavailable, "The service is temporarily unavailable.", internal)

	// The public Error() string must never contain internal detail.
	if strings.Contains(err.Error(), "db.internal") {
		t.Fatalf("SafeError.Error() leaked internal detail: %s", err.Error())
	}
	if err.Error() != "The service is temporarily unavailable." {
		t.Errorf("Error() = %q, want the safe user message", err.Error())
	}

	// But it IS available deliberately via Cause(), for logging code.
	if err.Cause().Error() != string(internal) {
		t.Errorf("Cause() = %v, want the internal error", err.Cause())
	}
}

func TestSafeErrorLogValueIncludesCause(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, slog.LevelInfo)
	ctx := context.Background()

	internal := errorString("raw db error detail")
	err := secure.Wrap(secure.CodeInternal, "Something went wrong.", internal)
	logger.Error(ctx, "operation failed", "error", err)

	// Server-side logs SHOULD contain the real cause -- that's the point
	// of SafeError.LogValue().
	if !strings.Contains(buf.String(), "raw db error detail") {
		t.Fatalf("expected internal cause in log output for observability, got: %s", buf.String())
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

type syncWriter struct {
	synced bool
}

func (w *syncWriter) Write(p []byte) (n int, err error) {
	return len(p), nil
}

func (w *syncWriter) Sync() error {
	w.synced = true
	return nil
}

func TestLoggerSync(t *testing.T) {
	sw := &syncWriter{}
	logger := logkit.New(logkit.Config{
		Format: logkit.FormatJSON,
		Writer: sw,
		Level:  slog.LevelInfo,
	})

	if err := logger.Sync(); err != nil {
		t.Fatalf("logger.Sync() failed: %v", err)
	}

	if !sw.synced {
		t.Errorf("expected underlying writer Sync to be called")
	}
}

func TestAdvancedRedaction(t *testing.T) {
	t.Run("nested path exact match", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := logkit.New(logkit.Config{
			Format: logkit.FormatJSON,
			Writer: buf,
			Level:  slog.LevelInfo,
			Redact: &logkit.RedactConfig{
				Paths: []string{"req.headers.authorization"},
			},
		})
		ctx := context.Background()
		logger.Info(ctx, "req", slog.Group("req",
			slog.Group("headers",
				slog.String("authorization", "bearer-token-123"),
				slog.String("content-type", "application/json"),
			),
			slog.String("path", "/users"),
		))

		line := decodeLastLine(t, buf)
		req, ok := line["req"].(map[string]any)
		if !ok {
			t.Fatalf("req not found in log output")
		}
		headers, ok := req["headers"].(map[string]any)
		if !ok {
			t.Fatalf("headers not found")
		}
		if headers["authorization"] != "[Redacted]" {
			t.Errorf("expected authorization to be [Redacted], got %v", headers["authorization"])
		}
		if headers["content-type"] != "application/json" {
			t.Errorf("expected content-type to be application/json, got %v", headers["content-type"])
		}
		if req["path"] != "/users" {
			t.Errorf("expected path to be /users, got %v", req["path"])
		}
	})

	t.Run("wildcard segments", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := logkit.New(logkit.Config{
			Format: logkit.FormatJSON,
			Writer: buf,
			Level:  slog.LevelInfo,
			Redact: &logkit.RedactConfig{
				Paths: []string{"*.password", "stuff.thats[*].secret", "path[\"with-hyphen\"]"},
			},
		})
		ctx := context.Background()
		logger.Info(ctx, "wildcards",
			slog.Group("user", slog.String("password", "123")),
			slog.Group("admin", slog.String("password", "456")),
			slog.Group("stuff", slog.Group("thats", slog.String("secret", "ssh"))),
			slog.Group("path", slog.String("with-hyphen", "hyphen")),
		)

		line := decodeLastLine(t, buf)
		user := line["user"].(map[string]any)
		admin := line["admin"].(map[string]any)
		stuff := line["stuff"].(map[string]any)
		thats := stuff["thats"].(map[string]any)
		path := line["path"].(map[string]any)

		if user["password"] != "[Redacted]" {
			t.Errorf("user.password = %v, want [Redacted]", user["password"])
		}
		if admin["password"] != "[Redacted]" {
			t.Errorf("admin.password = %v, want [Redacted]", admin["password"])
		}
		if thats["secret"] != "[Redacted]" {
			t.Errorf("stuff.thats.secret = %v, want [Redacted]", thats["secret"])
		}
		if path["with-hyphen"] != "[Redacted]" {
			t.Errorf("path.with-hyphen = %v, want [Redacted]", path["with-hyphen"])
		}
	})

	t.Run("custom censor and key removal", func(t *testing.T) {
		buf := &bytes.Buffer{}
		loggerCensor := logkit.New(logkit.Config{
			Format: logkit.FormatJSON,
			Writer: buf,
			Level:  slog.LevelInfo,
			Redact: &logkit.RedactConfig{
				Paths:  []string{"token"},
				Censor: "HIDDEN",
			},
		})
		ctx := context.Background()
		loggerCensor.Info(ctx, "censor", "token", "abc")
		lineCensor := decodeLastLine(t, buf)
		if lineCensor["token"] != "HIDDEN" {
			t.Errorf("token = %v, want HIDDEN", lineCensor["token"])
		}

		buf.Reset()
		loggerRemove := logkit.New(logkit.Config{
			Format: logkit.FormatJSON,
			Writer: buf,
			Level:  slog.LevelInfo,
			Redact: &logkit.RedactConfig{
				Paths:  []string{"token", "nested.secret"},
				Remove: true,
			},
		})
		loggerRemove.Info(ctx, "remove",
			slog.String("token", "abc"),
			slog.String("keep", "xyz"),
			slog.Group("nested", slog.String("secret", "123"), slog.String("ok", "456")),
		)
		lineRemove := decodeLastLine(t, buf)
		if _, exists := lineRemove["token"]; exists {
			t.Errorf("expected token key to be completely removed, but it exists")
		}
		if lineRemove["keep"] != "xyz" {
			t.Errorf("keep = %v, want xyz", lineRemove["keep"])
		}
		nested := lineRemove["nested"].(map[string]any)
		if _, exists := nested["secret"]; exists {
			t.Errorf("expected nested.secret to be removed, but it exists")
		}
		if nested["ok"] != "456" {
			t.Errorf("nested.ok = %v, want 456", nested["ok"])
		}
	})
}

func TestCallerSkip(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := logkit.New(logkit.Config{
		Format:    logkit.FormatJSON,
		Writer:    buf,
		Level:     slog.LevelInfo,
		AddSource: true,
	})

	logHelper := func(ctx context.Context, l *logkit.Logger, msg string) {
		l.Info(ctx, msg)
	}

	ctx := context.Background()
	logHelper(ctx, logger, "test helper normal")

	lineNormal := decodeLastLine(t, buf)
	sourceNormal := lineNormal["source"].(map[string]any)
	if !strings.Contains(sourceNormal["function"].(string), "TestCallerSkip.func") {
		t.Errorf("expected source to be inside helper, got %v", sourceNormal["function"])
	}

	buf.Reset()
	skippedLogger := logger.WithCallerSkip(1)
	logHelper(ctx, skippedLogger, "test helper skipped")

	lineSkipped := decodeLastLine(t, buf)
	sourceSkipped := lineSkipped["source"].(map[string]any)
	// The function name should be the outer TestCallerSkip (or TestCallerSkip.func1 in some compilers),
	// but it must skip the inner func1 which is the logHelper.
	if !strings.Contains(sourceSkipped["function"].(string), "TestCallerSkip") {
		t.Errorf("expected source to skip helper, got %v", sourceSkipped["function"])
	}
}
