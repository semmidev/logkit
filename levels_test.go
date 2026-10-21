package logkit

import (
	"log/slog"
	"testing"
)

// LevelString delegates to slogx.LevelName (single source of truth for
// the table); test the public behavior end-to-end here.
func TestLevelString(t *testing.T) {
	cases := map[slog.Level]string{
		LevelTrace:          "TRACE",
		slog.LevelDebug:     "DEBUG",
		slog.LevelInfo:      "INFO",
		slog.LevelWarn:      "WARN",
		slog.LevelError:     "ERROR",
		LevelFatal:          "FATAL",
		slog.LevelInfo + 2:  "INFO+2", // offsets fall back to slog's String() buckets
		slog.LevelWarn - 1:  "INFO+3",
		slog.LevelDebug - 3: "DEBUG-3",
	}
	for lvl, want := range cases {
		if got := LevelString(lvl); got != want {
			t.Errorf("LevelString(%d) = %q, want %q", int(lvl), got, want)
		}
	}
}

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
	}{
		{"trace", LevelTrace},
		{"debug", slog.LevelDebug},
		{"", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"fatal", LevelFatal},
		{"TRACE", LevelTrace},
		{"Fatal", LevelFatal},
	}
	for _, tc := range cases {
		lvl, ok := ParseLevel(tc.in)
		if !ok || lvl != tc.want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v, true", tc.in, lvl, ok, tc.want)
		}
	}
	if _, ok := ParseLevel("bogus"); ok {
		t.Error("ParseLevel(bogus) must not report ok")
	}
}
