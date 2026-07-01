// Package sampler implements the tail-sampling pattern described in
// https://loggingsucks.com/: make the "keep or drop this event" decision
// *after* the request completes, based on its outcome, instead of
// randomly sampling up front and risking dropping the exact request
// that would have explained an outage.
package sampler

// Decider decides whether a completed wide event / canonical log line
// should be kept (emitted) or dropped. Implementations receive the fully
// built event (see event.Event.Snapshot) so the decision can inspect
// status code, duration, user tier, feature flags -- anything that ended
// up in the event.
type Decider interface {
	ShouldSample(fields map[string]any) bool
}

// DeciderFunc adapts a plain function to the Decider interface.
type DeciderFunc func(fields map[string]any) bool

func (f DeciderFunc) ShouldSample(fields map[string]any) bool { return f(fields) }

// Always keeps every event. Reasonable default for low/medium traffic
// services, or for the "errors and warnings" handler in a multi-handler
// setup where you always want the full picture.
var Always Decider = DeciderFunc(func(map[string]any) bool { return true })

// Never drops every event. Occasionally useful for a handler you want to
// disable via config without removing it from the pipeline.
var Never Decider = DeciderFunc(func(map[string]any) bool { return false })
