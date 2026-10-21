package logkit

import (
	"log/slog"
	"strings"
)

// Custom levels that extend the four built-in slog levels
// (Debug=-4, Info=0, Warn=4, Error=8).
//
// slog.Level is just an int, so adding new levels is a matter of picking
// values that sort correctly relative to the built-ins. This is the
// officially recommended way to extend slog's leveling
// (see log/slog package docs, "Levels").
const (
	// LevelTrace is for extremely verbose, per-iteration detail that is
	// normally too noisy even for Debug (e.g. raw wire bytes, tight loop
	// state). Sits below Debug.
	LevelTrace slog.Level = slog.LevelDebug - 4 // -8

	// LevelFatal is for errors that are about to terminate the process.
	// Logging at this level does NOT call os.Exit by itself -- that
	// decision belongs to the caller (see Logger.Fatal), so that the
	// logger stays testable and side-effect free.
	LevelFatal slog.Level = slog.LevelError + 4 // 12
)

// levelNames maps custom levels (and the built-ins, for completeness) to
// the string that should be emitted for the "level" attribute. Handlers
// built with slogx.NewJSONHandler / NewConsoleHandler use this via
// ReplaceAttr so custom levels print as "TRACE"/"FATAL" instead of
// falling back to slog's "DEBUG-4"/"ERROR+4" default formatting.
var levelNames = map[slog.Level]string{
	LevelTrace:      "TRACE",
	slog.LevelDebug: "DEBUG",
	slog.LevelInfo:  "INFO",
	slog.LevelWarn:  "WARN",
	slog.LevelError: "ERROR",
	LevelFatal:      "FATAL",
}

// LevelString renders lvl using the extended name table, falling back to
// slog's default String() for anything it doesn't recognize (including
// levels offset from the named ones, e.g. "INFO+2").
func LevelString(lvl slog.Level) string {
	if name, ok := levelNames[lvl]; ok {
		return name
	}
	return lvl.String()
}

// ParseLevel parses a case-insensitive level name ("trace", "debug",
// "info", "warn"/"warning", "error", "fatal") into a slog.Level. This is
// the counterpart to LevelString and is handy for reading the level out
// of an env var or config file.
func ParseLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(s) {
	case "trace":
		return LevelTrace, true
	case "debug":
		return slog.LevelDebug, true
	case "info", "":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	case "fatal":
		return LevelFatal, true
	default:
		return slog.LevelInfo, false
	}
}
