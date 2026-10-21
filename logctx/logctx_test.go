package logctx_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/semmidev/logkit/logctx"
)

func TestIDsRoundTrip(t *testing.T) {
	ctx := context.Background() // unrelated key
	ctx = logctx.WithRequestID(ctx, "req-1")
	ctx = logctx.WithCorrelationID(ctx, "corr-1")
	if logctx.RequestID(ctx) != "req-1" || logctx.CorrelationID(ctx) != "corr-1" {
		t.Fatal("ids not round-tripping; other ctx keys must not collide")
	}
}

func TestTraceSpanIDs(t *testing.T) {
	ctx := logctx.WithTraceID(logctx.WithSpanID(context.Background(), "span"), "trace")
	if logctx.TraceID(ctx) != "trace" || logctx.SpanID(ctx) != "span" {
		t.Fatal("trace/span ids not round-tripping")
	}
}

func TestNewIDIsUUIDv4Shape(t *testing.T) {
	id := logctx.NewID()
	if len(id) != 36 {
		t.Fatalf("id = %q, want 36 chars", id)
	}
	if id[8] != '-' || id[13] != '-' || id[14] != '4' || id[18] != '-' || id[19] != '8' && id[19] != '9' && id[19] != 'a' && id[19] != 'b' {
		t.Fatalf("id = %q, not UUIDv4 shape", id)
	}
}

func TestEnrichWithoutBagIsNoOp(t *testing.T) {
	ctx := context.Background()
	ctx = logctx.WithRequestID(ctx, "req-1")
	logctx.Enrich(ctx, "k", "v") // no WithFields: must not panic, adds nothing
	if logctx.FieldsFromContext(ctx) != nil {
		t.Fatal("Enrich on ctx without fields bag must be a no-op")
	}
}

func TestEnrichConcurrent(t *testing.T) {
	ctx := logctx.WithFields(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			logctx.Enrich(ctx, "k", "v")
		}()
	}
	wg.Wait()
	if got := len(logctx.FieldsFromContext(ctx)); got != 64 { // 32 × (k, v)
		t.Fatalf("got %d args, want 64", got)
	}
}

func TestAddFieldsToRecord(t *testing.T) {
	ctx := logctx.WithFields(context.Background())
	logctx.Enrich(ctx, "user_id", "u-1")
	var r slog.Record
	logctx.AddFieldsToRecord(ctx, &r)

	var vals []string
	r.Attrs(func(a slog.Attr) bool {
		vals = append(vals, a.Key)
		return true
	})
	if len(vals) != 1 || vals[0] != "user_id" {
		t.Fatalf("record attrs = %v, want [user_id]", vals)
	}
}
