// Package slogx contains ready-to-use slog.Handler implementations
// (JSON for production, colorized console for local dev, and a
// fan-out multi-handler) plus the small pieces of handler-level
// plumbing -- key-based redaction and dynamic level -- that most
// services end up needing.
//
// Every constructor here returns a plain slog.Handler, so the extension
// point stays exactly where the standard library put it: nothing stops
// you from writing your own slog.Handler and passing it to
// logkit.New(logkit.WithHandler(yours)) instead of anything in this
// package.
package slogx

import (
	"log/slog"
	"strings"
)

// RedactConfig configures advanced, path-based log redaction.
type RedactConfig struct {
	// Paths specifies the keys or dot-notated paths to redact.
	// Supports wildcards (e.g., "*.password", "stuff.thats[*].secret", "user.*").
	Paths []string

	// Censor is the value to replace sensitive data with. Defaults to "[Redacted]" if empty.
	Censor string

	// Remove, if true, completely removes the matched keys and values from logs
	// instead of censoring them.
	Remove bool
}

// HandlerOptions configures the handlers built by this package. It
// intentionally mirrors slog.HandlerOptions plus the extra knobs logkit
// adds (sensitive-key redaction), so switching between NewJSONHandler and
// NewConsoleHandler is a one-line change.
type HandlerOptions struct {
	// Level filters output. Pass a *slog.LevelVar (via logkit.Logger.Leveler())
	// if you want to change verbosity at runtime; a plain slog.Level also
	// works for a fixed level. Defaults to slog.LevelInfo.
	Level slog.Leveler

	// AddSource includes the file:line of the log call. Useful in dev,
	// often turned off in production for performance/log-volume reasons.
	AddSource bool

	// SensitiveKeys is a defense-in-depth, belt-and-suspenders redaction
	// list: any attribute (at any nesting depth) whose key case-insensitively
	// matches one of these is replaced with "***REDACTED***" before it
	// reaches the sink -- regardless of whether the value's type
	// implements slog.LogValuer.
	//
	// This is NOT a substitute for secure.Secret[T] / secure.Redactor
	// (those stop the sensitive value from ever being formatted in the
	// first place, which is strictly safer). Treat SensitiveKeys as a
	// safety net that catches the case a developer logs
	// slog.String("password", raw) directly instead of wrapping it.
	SensitiveKeys []string

	// Redact configures advanced, Pino-like redaction behavior. If set,
	// it takes precedence over SensitiveKeys.
	Redact *RedactConfig

	// ReplaceAttr, if set, runs *after* the built-in level-name and
	// sensitive-key handling, exactly like slog.HandlerOptions.ReplaceAttr.
	// Use it for anything else you need (dropping a field entirely by
	// returning slog.Attr{}, renaming keys to match a log schema, etc).
	ReplaceAttr func(groups []string, a slog.Attr) slog.Attr
}

func (o HandlerOptions) toStdlib() *slog.HandlerOptions {
	sensitive := make(map[string]struct{}, len(o.SensitiveKeys))
	for _, k := range o.SensitiveKeys {
		sensitive[strings.ToLower(k)] = struct{}{}
	}

	level := o.Level
	if level == nil {
		level = slog.LevelInfo
	}

	return &slog.HandlerOptions{
		AddSource: o.AddSource,
		Level:     level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// 1. Render custom levels ("TRACE"/"FATAL") with proper names.
			if a.Key == slog.LevelKey {
				if lvl, ok := a.Value.Any().(slog.Level); ok {
					a.Value = slog.StringValue(LevelName(lvl))
				}
			}
			// 2. Defense-in-depth redaction.
			var shouldRedact bool
			censorVal := "***REDACTED***"
			var removeVal bool

			if o.Redact != nil && len(o.Redact.Paths) > 0 {
				censorVal = "[Redacted]"
				if o.Redact.Censor != "" {
					censorVal = o.Redact.Censor
				}
				removeVal = o.Redact.Remove

				for _, pattern := range o.Redact.Paths {
					if pathMatches(pattern, groups, a.Key) {
						shouldRedact = true
						break
					}
				}
			} else if len(sensitive) > 0 {
				if _, hit := sensitive[strings.ToLower(a.Key)]; hit {
					shouldRedact = true
				}
			}

			if shouldRedact {
				if removeVal {
					return slog.Attr{} // Completely discard the attribute
				}
				a.Value = slog.StringValue(censorVal)
			}

			// 3. User-supplied hook gets the final say.
			if o.ReplaceAttr != nil {
				a = o.ReplaceAttr(groups, a)
			}
			return a
		},
	}
}

// LevelName renders lvl using the extended name table. Single source of
// truth for level naming: logkit.LevelString delegates here, and both
// slogx handlers use it for the "level" attribute, so custom levels
// print as "TRACE"/"FATAL" instead of slog's "DEBUG-4"/"ERROR+4"
// default. Falls back to slog's String() for anything unnamed (e.g.
// "INFO+2").
func LevelName(l slog.Level) string {
	switch l {
	case slog.LevelDebug - 4:
		return "TRACE"
	case slog.LevelDebug:
		return "DEBUG"
	case slog.LevelInfo:
		return "INFO"
	case slog.LevelWarn:
		return "WARN"
	case slog.LevelError:
		return "ERROR"
	case slog.LevelError + 4:
		return "FATAL"
	default:
		return l.String()
	}
}

// pathMatches reports whether groups+key matches pattern.
// Pattern can be simple (e.g. "password"), dot-notated (e.g. "req.headers.authorization"),
// or contain wildcards (e.g. "stuff.thats[*].secret", "*.password", "user.*").
func pathMatches(pattern string, groups []string, key string) bool {
	pattern = strings.ToLower(pattern)
	pattern = strings.ReplaceAll(pattern, "[\"", ".")
	pattern = strings.ReplaceAll(pattern, "\"]", "")
	pattern = strings.ReplaceAll(pattern, "['", ".")
	pattern = strings.ReplaceAll(pattern, "']", "")
	pattern = strings.ReplaceAll(pattern, "[*]", "")
	pattern = strings.ReplaceAll(pattern, "[", ".")
	pattern = strings.ReplaceAll(pattern, "]", "")
	pattern = strings.ReplaceAll(pattern, "..", ".")
	pattern = strings.Trim(pattern, ".")

	var fullPath string
	if len(groups) > 0 {
		var cleanGroups []string
		for _, g := range groups {
			if g != "" {
				cleanGroups = append(cleanGroups, strings.ToLower(g))
			}
		}
		if len(cleanGroups) > 0 {
			fullPath = strings.Join(cleanGroups, ".") + "." + strings.ToLower(key)
		} else {
			fullPath = strings.ToLower(key)
		}
	} else {
		fullPath = strings.ToLower(key)
	}

	if !strings.Contains(pattern, ".") {
		return strings.EqualFold(key, pattern) || strings.HasSuffix(fullPath, "."+pattern)
	}

	if strings.Contains(pattern, "*") {
		return matchWildcard(pattern, fullPath)
	}

	return fullPath == pattern
}

func matchWildcard(pattern, path string) bool {
	patternSegs := strings.Split(pattern, ".")
	pathSegs := strings.Split(path, ".")
	return matchSegments(patternSegs, pathSegs)
}

func matchSegments(patternSegs, pathSegs []string) bool {
	if len(patternSegs) == 0 && len(pathSegs) == 0 {
		return true
	}
	if len(patternSegs) == 0 || len(pathSegs) == 0 {
		if len(patternSegs) == 1 && patternSegs[0] == "*" {
			return true
		}
		return false
	}

	p := patternSegs[0]
	if p == "*" {
		for i := 0; i <= len(pathSegs); i++ {
			if matchSegments(patternSegs[1:], pathSegs[i:]) {
				return true
			}
		}
		return false
	}

	if strings.Contains(p, "*") {
		if matchStringWildcard(p, pathSegs[0]) {
			return matchSegments(patternSegs[1:], pathSegs[1:])
		}
		return false
	}

	if p == pathSegs[0] {
		return matchSegments(patternSegs[1:], pathSegs[1:])
	}

	return false
}

func matchStringWildcard(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(s, parts[i])
		if idx == -1 {
			return false
		}
		s = s[idx+len(parts[i]):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}
