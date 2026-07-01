// Example: an HTTP server using httpmw to get one canonical log line per
// request, automatic request-id propagation, panic recovery, and secure
// error responses -- all pluggable via a plain func(http.Handler) http.Handler,
// so this drops straight into chi (r.Use(mw)) with no adapter.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/semmidev/logkit"
	"github.com/semmidev/logkit/event"
	"github.com/semmidev/logkit/logctx"
	"github.com/semmidev/logkit/middleware/httpmw"
	"github.com/semmidev/logkit/sampler"
	"github.com/semmidev/logkit/secure"
	"github.com/semmidev/logkit/slogx"
)

func main() {
	// Create an asynchronous buffered writer for high-performance production logging.
	asyncWriter := slogx.NewAsyncWriter(os.Stdout, 8192, 100*time.Millisecond)
	defer asyncWriter.Close()

	logger := logkit.New(logkit.Config{
		Format:      logkit.FormatJSON,
		Writer:      asyncWriter, // Pipe through our high-performance async writer
		Level:       slog.LevelInfo,
		ServiceName: "orders-api",
		Version:     "0.9.0",
		Environment: "development",
		AddSource:   true,
		// Configure advanced path redaction matching Pino (e.g. nested authorization headers and credit card numbers)
		Redact: &logkit.RedactConfig{
			Paths: []string{
				"req.headers.authorization", // redact authorization headers
				"card.number",               // redact credit card numbers nested in card group
				"*.password",                // redact passwords nested at any depth
			},
			Censor: "[CONFIDENTIAL]",
		},
	})
	defer logger.Sync()

	mw := httpmw.Middleware(httpmw.Config{
		Logger: logger,
		// Always keep errors/5xx/slow (default rule set), and 5% of the
		// rest, once volume warrants sampling. Left at sampler.Always
		// (the httpmw default) here since this is a toy example.
		Sampler: sampler.NewTailSampler(500 /* ms */, 0.05),
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/orders/", handleGetOrder)
	mux.HandleFunc("/panic", handlePanicDemo)

	handler := mw(mux)

	logger.Info(context.Background(), "listening", "addr", ":8080")
	if err := http.ListenAndServe(":8080", handler); err != nil {
		logger.Fatal(context.Background(), "server stopped", "error", err)
	}
}

func handleGetOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Enrich the request's wide event with business context as we learn
	// it -- this is what turns a bare "GET /orders/42 -> 200" into a rich,
	// queryable event without adding more log lines.
	orderID := r.URL.Path[len("/orders/"):]
	event.Enrich(ctx, "order.id", orderID)
	logctx.Enrich(ctx, "handler", "handleGetOrder")

	// Enrich the event with a nested card group to demonstrate advanced path redaction
	// of sensitive fields (card.number will be redacted, card.holder will remain).
	event.Enrich(ctx, "card", slog.GroupValue(
		slog.String("number", "1234-5678-9012-3456"),
		slog.String("holder", "John Doe"),
	))

	order, err := lookupOrder(ctx, orderID)
	if err != nil {
		// One call handles logging the real cause AND writing a safe,
		// generic response -- the "crossing public boundaries" step.
		httpmw.RespondError(ctx, w, nil, err)
		return
	}

	event.Enrich(ctx, "order.total_cents", order.TotalCents)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"id":"` + order.ID + `","total_cents":` + itoa(order.TotalCents) + `}`))
}

func handlePanicDemo(w http.ResponseWriter, r *http.Request) {
	// Demonstrates fail-secure behavior: the middleware recovers this,
	// logs the real panic value, and returns a generic 500 -- the client
	// never sees the panic message or a stack trace.
	panic("simulated inconsistent state")
}

type order struct {
	ID         string
	TotalCents int
}

func lookupOrder(ctx context.Context, id string) (*order, error) {
	if id == "999" {
		// Raw, detail-leaking error from a hypothetical DB layer.
		return nil, secure.Wrap(secure.CodeNotFound, "Order not found.",
			errors.New(`pq: no rows in result set for orders where id=999`))
	}
	return &order{ID: id, TotalCents: 15999}, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
