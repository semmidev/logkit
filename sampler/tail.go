package sampler

import "math/rand"

// Rule is a single "always keep if..." predicate evaluated against a
// completed event's fields. TailSampler runs every Rule before falling
// back to random sampling, so extending the sampling policy (e.g. "also
// always keep requests from our internal QA tenant") is a matter of
// appending a Rule -- no need to reimplement TailSampler.
type Rule func(fields map[string]any) bool

// TailSampler is the default Decider: always keep anything matched by
// one of its Rules (errors, slow requests, VIP users, ... -- whatever
// you configure), otherwise keep a random Rate fraction of the rest.
// This mirrors the pattern from loggingsucks.com almost verbatim.
type TailSampler struct {
	// Rules are evaluated in order; the first match wins and the event
	// is kept. If no rule matches, the Rate-based random decision runs.
	Rules []Rule

	// Rate is the fraction (0.0-1.0) of non-matching events to keep.
	// E.g. 0.05 keeps 5% of "boring" (fast, successful) events, which is
	// usually enough to compute latency/throughput baselines without
	// paying to store 100% of uninteresting traffic.
	Rate float64

	// Rand is used for the random decision. Defaults to a package-level
	// source if nil. Overridable for deterministic tests.
	Rand *rand.Rand
}

// NewTailSampler builds a TailSampler with a sensible starting policy:
// always keep errors (an "error" field is present/truthy, or
// status_code >= 500) and requests slower than slowMS, random-sample the
// rest at rate. Call .Rules = append(s.Rules, ...) afterwards to layer on
// more conditions (VIP users, specific feature flags being debugged,
// etc) -- see the package doc example.
func NewTailSampler(slowMS float64, rate float64) *TailSampler {
	return &TailSampler{
		Rate: rate,
		Rules: []Rule{
			RuleHasError,
			RuleStatusAtLeast(500),
			RuleSlowerThan(slowMS),
		},
	}
}

func (s *TailSampler) ShouldSample(fields map[string]any) bool {
	for _, rule := range s.Rules {
		if rule(fields) {
			return true
		}
	}
	if s.Rand != nil {
		// Field is for deterministic tests; it is NOT concurrency-safe,
		// so only use it when the caller supplied one.
		return s.Rand.Float64() < s.Rate
	}
	// Top-level rand is goroutine-safe; rand.New sources are not.
	return rand.Float64() < s.Rate
}

// --- Built-in rules -------------------------------------------------

// RuleHasError keeps any event carrying a non-empty "error" field.
func RuleHasError(fields map[string]any) bool {
	v, ok := fields["error"]
	if !ok || v == nil {
		return false
	}
	if s, ok := v.(string); ok {
		return s != ""
	}
	return true
}

// RuleStatusAtLeast keeps events whose numeric "status_code" field is
// >= min (e.g. RuleStatusAtLeast(500) for server errors).
func RuleStatusAtLeast(min int) Rule {
	return func(fields map[string]any) bool {
		code, ok := asInt(fields["status_code"])
		return ok && code >= min
	}
}

// RuleSlowerThan keeps events whose "duration_ms" field exceeds
// thresholdMS.
func RuleSlowerThan(thresholdMS float64) Rule {
	return func(fields map[string]any) bool {
		d, ok := asFloat(fields["duration_ms"])
		return ok && d > thresholdMS
	}
}

// RuleFieldEquals keeps events where fields[key] == value -- handy for
// "always keep our internal test tenant" or "always keep this rollout's
// feature flag".
func RuleFieldEquals(key string, value any) Rule {
	return func(fields map[string]any) bool {
		v, ok := fields[key]
		return ok && v == value
	}
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}
