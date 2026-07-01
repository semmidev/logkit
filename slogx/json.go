package slogx

import (
	"io"
	"log/slog"
)

// NewJSONHandler returns a slog.Handler that writes newline-delimited
// JSON to w -- the format you want in production (machine-parseable,
// ships cleanly to Loki/ClickHouse/Elasticsearch/whatever your log
// pipeline is).
func NewJSONHandler(w io.Writer, opts HandlerOptions) slog.Handler {
	return slog.NewJSONHandler(w, opts.toStdlib())
}
