package httpmw_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/semmidev/logkit"
	"github.com/semmidev/logkit/logctx"
	"github.com/semmidev/logkit/middleware/httpmw"
	"github.com/semmidev/logkit/secure"
)

type bufWriter struct {
	mu      sync.Mutex
	lines   []string
	flushed bool
}

func (b *bufWriter) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append(b.lines, string(p))
	return len(p), nil
}

func (b *bufWriter) Flush() { b.flushed = true }

func (b *bufWriter) last() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.lines) == 0 {
		return ""
	}
	return b.lines[len(b.lines)-1]
}

func lastLineJSON(t *testing.T, w *bufWriter) map[string]any {
	t.Helper()
	line := w.last()
	var out map[string]any
	if err := json.Unmarshal([]byte(line), &out); err != nil {
		t.Fatalf("bad log line %q: %v", line, err)
	}
	return out
}

func testLogger(w *bufWriter) *logkit.Logger {
	return logkit.New(logkit.Config{Writer: w})
}

func TestMiddlewareEmitsOneLinePerRequest(t *testing.T) {
	w := &bufWriter{}
	mw := httpmw.Middleware(httpmw.Config{Logger: testLogger(w)})
	srv := mw(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		logctx.Enrich(r.Context(), "user_id", "u-1")
		rw.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))

	line := lastLineJSON(t, w)
	if line["http.status_code"] != float64(200) || line["user_id"] != "u-1" {
		t.Fatalf("canonical line wrong: %v", line)
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Fatal("request id must be echoed back on the response")
	}
	// exactly one log line for the whole request
	if len(w.lines) != 1 {
		t.Fatalf("got %d lines, want 1 canonical line", len(w.lines))
	}
}

func TestMiddlewarePanicFailsSecure(t *testing.T) {
	w := &bufWriter{}
	mw := httpmw.Middleware(httpmw.Config{Logger: testLogger(w)})
	srv := mw(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		panic("simulated inconsistent state")
	}))

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))

	if rec.Code != 500 || strings.Contains(rec.Body.String(), "simulated inconsistent state") {
		t.Fatalf("panic leaked to client: %d %q", rec.Code, rec.Body.String())
	}
	line := lastLineJSON(t, w)
	if line["outcome"] != "error" || line["http.status_code"] != float64(500) {
		t.Fatalf("panic not attached to event: %v", line)
	}
}

func TestMiddleware4xxWarn5xxError(t *testing.T) {
	w := &bufWriter{}
	mw := httpmw.Middleware(httpmw.Config{Logger: testLogger(w)})
	srv := mw(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(404)
	}))
	srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	line := lastLineJSON(t, w)
	if line["level"] != "WARN" {
		t.Fatalf("404 should log WARN, got %v", line["level"])
	}
}

func TestStatusWriterFlusher(t *testing.T) {
	// SSE-style handler must see the wrapper as an http.Flusher.
	w := &bufWriter{}
	mw := httpmw.Middleware(httpmw.Config{Logger: testLogger(w)})
	flushed := false
	srv := mw(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		f, ok := rw.(http.Flusher)
		if !ok {
			t.Fatal("handler ResponseWriter must implement http.Flusher")
		}
		f.Flush()
		flushed = true
	}))
	srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	if !flushed {
		t.Fatal("Flush never ran")
	}
}

func TestRespondErrorNeverLeaks(t *testing.T) {
	w := &bufWriter{}
	_ = w
	rec := httptest.NewRecorder()
	raw := errors.New("pq: no rows for orders where id=42 AND internal secret")

	httpmw.RespondError(context.Background(), rec, nil, raw)

	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "no rows") || strings.Contains(body, "internal secret") {
		t.Fatalf("RespondError leaked internal detail: %q", body)
	}
	var resp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad response body %q: %v", body, err)
	}
	if resp.Code != secure.CodeInternal {
		t.Fatalf("code = %q, want INTERNAL", resp.Code)
	}
}
