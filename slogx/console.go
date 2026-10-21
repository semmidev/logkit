package slogx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

// ANSI color codes. Kept minimal and dependency-free on purpose --
// pulling in a color library for this would be overkill, and it means
// consoleHandler has zero third-party imports.
const (
	ansiReset  = "\x1b[0m"
	ansiGray   = "\x1b[90m"
	ansiRed    = "\x1b[31m"
	ansiRedBg  = "\x1b[97;41m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
	ansiBlue   = "\x1b[34m"
	ansiBold   = "\x1b[1m"
)

func levelColor(l slog.Level) string {
	switch LevelName(l) {
	case "TRACE":
		return ansiGray
	case "DEBUG":
		return ansiBlue
	case "INFO":
		return ansiCyan
	case "WARN":
		return ansiYellow
	case "ERROR":
		return ansiRed
	case "FATAL":
		return ansiRedBg
	default:
		return ansiReset
	}
}

// consoleHandler is a slog.Handler tuned for a human staring at a
// terminal during local development: colorized level, short timestamp,
// message, then "key=value" pairs, groups rendered as dotted prefixes.
// For production use NewJSONHandler instead -- this handler prioritizes
// readability over parse-ability and is not meant to be shipped to a log
// pipeline.
type consoleHandler struct {
	mu      *sync.Mutex
	w       io.Writer
	opts    HandlerOptions
	std     *slog.HandlerOptions
	attrs   []slog.Attr // accumulated via WithAttrs
	groups  []string    // accumulated via WithGroup
	noColor bool
}

// NewConsoleHandler returns a slog.Handler that writes a colorized,
// human-friendly line per record to w. Color is automatically part of
// the output; wrap w so it strips ANSI codes (or set opts via a custom
// ReplaceAttr) if you need plain text, e.g. when w is a file rather than
// a terminal.
func NewConsoleHandler(w io.Writer, opts HandlerOptions) slog.Handler {
	return &consoleHandler{
		mu:   &sync.Mutex{},
		w:    w,
		opts: opts,
		std:  opts.toStdlib(),
	}
}

func (h *consoleHandler) Enabled(_ context.Context, level slog.Level) bool {
	minLevel := slog.LevelInfo
	if h.std.Level != nil {
		minLevel = h.std.Level.Level()
	}
	return level >= minLevel
}

func (h *consoleHandler) Handle(_ context.Context, r slog.Record) error {
	var buf bytes.Buffer

	// Timestamp (dimmed).
	if !r.Time.IsZero() {
		buf.WriteString(ansiGray)
		buf.WriteString(r.Time.Format(time.TimeOnly))
		buf.WriteString(ansiReset)
		buf.WriteByte(' ')
	}

	// Level, colorized and padded.
	lvl := LevelName(r.Level)
	buf.WriteString(levelColor(r.Level))
	buf.WriteString(ansiBold)
	fmt.Fprintf(&buf, "%-5s", lvl)
	buf.WriteString(ansiReset)
	buf.WriteByte(' ')

	// Message.
	buf.WriteString(r.Message)

	// Collect attrs: handler-level (from WithAttrs) + record-level, both
	// honoring accumulated groups, then apply ReplaceAttr the same way
	// slog's own handlers do, then print sorted for deterministic,
	// scannable output.
	fields := map[string]string{}
	var addAttr func(groups []string, prefix string, a slog.Attr)
	addAttr = func(groups []string, prefix string, a slog.Attr) {
		a = resolveReplaceAttr(h.std, groups, a)
		if a.Equal(slog.Attr{}) {
			return
		}

		if a.Value.Kind() == slog.KindGroup {
			newGroups := append(append([]string{}, groups...), a.Key)
			newPrefix := a.Key
			if prefix != "" {
				newPrefix = prefix + "." + a.Key
			}
			for _, child := range a.Value.Group() {
				addAttr(newGroups, newPrefix, child)
			}
			return
		}

		key := a.Key
		if prefix != "" {
			key = prefix + "." + key
		}
		fields[key] = formatValue(a.Value)
	}

	for _, a := range h.attrs {
		addAttr(h.groups, "", a)
	}
	r.Attrs(func(a slog.Attr) bool {
		addAttr(h.groups, "", a)
		return true
	})

	if len(fields) > 0 {
		keys := make([]string, 0, len(fields))
		for k := range fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			buf.WriteByte(' ')
			buf.WriteString(ansiGray)
			buf.WriteString(k)
			buf.WriteByte('=')
			buf.WriteString(ansiReset)
			buf.WriteString(fields[k])
		}
	}

	// Source location, if enabled.
	if h.opts.AddSource && r.PC != 0 {
		f := sourceFrame(r.PC)
		if f.File != "" {
			buf.WriteByte(' ')
			buf.WriteString(ansiGray)
			fmt.Fprintf(&buf, "(%s:%d)", f.File, f.Line)
			buf.WriteString(ansiReset)
		}
	}

	buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(buf.Bytes())
	return err
}

func (h *consoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	nh := *h
	nh.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &nh
}

func (h *consoleHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	nh := *h
	nh.groups = append(append([]string{}, h.groups...), name)
	return &nh
}

func resolveReplaceAttr(std *slog.HandlerOptions, groups []string, a slog.Attr) slog.Attr {
	if std.ReplaceAttr == nil {
		return a
	}
	return std.ReplaceAttr(groups, a)
}

func formatValue(v slog.Value) string {
	v = v.Resolve()
	s := v.String()
	if strings.ContainsAny(s, " \t\n\"") {
		return fmt.Sprintf("%q", s)
	}
	return s
}
