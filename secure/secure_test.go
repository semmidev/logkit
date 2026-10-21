package secure_test

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/semmidev/logkit/secure"
)

const internalDetail = "pq: connection refused to db.internal:5432"

func TestErrorReturnsOnlySafeMessage(t *testing.T) {
	err := secure.Wrap(secure.CodeUnavailable, "temporarily unavailable", errString(internalDetail))
	if err.Error() != "temporarily unavailable" || strings.Contains(err.Error(), internalDetail) {
		t.Fatalf("Error() leaked internals: %q", err.Error())
	}
	if err.Cause().Error() != internalDetail {
		t.Fatalf("Cause() = %q, want internal detail", err.Cause().Error())
	}
}

func TestOpaqueByDefault(t *testing.T) {
	err := secure.Wrap("CODE_X", "safe", errString("raw"))
	if err.Unwrap() != nil {
		t.Fatal("Wrap must be opaque: Unwrap must return nil")
	}
	var se *errString
	if errors.As(err, &se) {
		t.Fatal("errors.As must not traverse an opaque SafeError")
	}
	if !secure.Is(err, "CODE_X") || secure.Is(err, "OTHER") {
		t.Fatal("secure.Is must match exactly on Code")
	}
}

func TestTransparentUnwrap(t *testing.T) {
	err := secure.WrapTransparent("X", "safe", errString("raw"))
	if e := err.Unwrap(); e == nil || e.Error() != "raw" {
		t.Fatal("WrapTransparent must expose Internal via Unwrap")
	}
	if _, ok := secure.As(err); !ok {
		t.Fatal("secure.As must find the SafeError")
	}
}

func TestLogValueStructure(t *testing.T) {
	err := secure.New("AUTH_FAILED", "bad login", errString("raw"), map[string]string{"user": "sam"})
	v := err.LogValue().Resolve()
	joined := ""
	for _, a := range v.Group() {
		joined += a.Key + "=" + a.Value.String() + " "
	}
	for _, want := range []string{"code=AUTH_FAILED", "message=bad login", "cause=raw", "user=sam"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("LogValue missing %q, got %q", want, joined)
		}
	}
}

func TestEmptyUserMsgFallsBackSafely(t *testing.T) {
	err := secure.New(secure.CodeInternal, "", nil, nil)
	if err.Error() != "internal error" {
		t.Fatalf("Error() = %q, want safe fallback", err.Error())
	}
}

func TestSecretEverythingRedacts(t *testing.T) {
	s := secure.NewSecret("hunter2")
	if s.String() != "***REDACTED***" {
		t.Fatalf("String() = %q, want redacted", s.String())
	}
	if s.Reveal() != "hunter2" {
		t.Fatal("Reveal must return the raw value")
	}
	if s.LogValue().String() != "***REDACTED***" {
		t.Fatal("LogValue must redact")
	}
	b, _ := json.Marshal(map[string]any{"pw": s})
	if strings.Contains(string(b), "hunter2") {
		t.Fatalf("JSON leaked secret: %s", b)
	}
}

func TestRedactHelper(t *testing.T) {
	if secure.Redact(42) == nil {
		t.Fatal("non-Redactor value must pass through unchanged")
	}

	out := secure.Redact(redactorTest{})
	lv, ok := out.(slog.LogValuer)
	if !ok {
		t.Fatal("Redactor output must be wrapped as LogValuer")
	}
	if v := lv.LogValue().Resolve(); !v.Equal(slog.StringValue("safe!")) {
		t.Fatalf("LogValue = %v, want safe!", v)
	}
}

func TestGenericCodesStable(t *testing.T) {
	// Code values are part of the public API contract (clients branch on
	// them); a silent rename would break integrations.
	want := map[string]string{
		secure.CodeInternal:     "INTERNAL",
		secure.CodeNotFound:     "NOT_FOUND",
		secure.CodeInvalidInput: "INVALID_INPUT",
		secure.CodeUnauthorized: "UNAUTHORIZED",
		secure.CodeForbidden:    "FORBIDDEN",
		secure.CodeConflict:     "CONFLICT",
		secure.CodeUnavailable:  "UNAVAILABLE",
		secure.CodeTimeout:      "TIMEOUT",
	}
	for have, is := range want {
		if have != is {
			t.Fatalf("code = %q, want %q", have, is)
		}
	}
}

type redactorTest struct{}

func (redactorTest) Redact() any { return "safe!" }

type errString string

func (e errString) Error() string { return string(e) }
