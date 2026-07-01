package slogx

import "runtime"

// sourceFrame resolves a single runtime.Frame (file/line/function) from a
// program counter, mirroring what slog's built-in handlers do internally
// for AddSource but exposing just the bit consoleHandler needs.
func sourceFrame(pc uintptr) runtime.Frame {
	frames := runtime.CallersFrames([]uintptr{pc})
	f, _ := frames.Next()
	return f
}
