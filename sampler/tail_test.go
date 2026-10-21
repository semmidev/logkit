package sampler_test

import (
	"sync"
	"testing"

	"github.com/semmidev/logkit/sampler"
)

func TestTailSamplerRules(t *testing.T) {
	// Rate 0 removes the random factor; only the rules decide.
	s := sampler.NewTailSampler(100 /* ms */, 0)
	base := map[string]any{"status_code": 200, "duration_ms": 10.0}

	cases := []struct {
		name   string
		fields map[string]any
		want   bool
	}{
		{"boring fast success", base, false},
		{"error field kept", map[string]any{"error": "boom"}, true},
		{"500 kept", map[string]any{"status_code": 500, "duration_ms": 1.0}, true},
		{"slow kept", map[string]any{"status_code": 200, "duration_ms": 1000.0}, true},
		{"exactly at slow threshold dropped (strict >)", map[string]any{"status_code": 200, "duration_ms": 100.0}, false},
		{"int status code kept", map[string]any{"status_code": 503, "duration_ms": 1.0}, true},
	}
	for _, tc := range cases {
		if got := s.ShouldSample(tc.fields); got != tc.want {
			t.Errorf("%s: ShouldSample = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTailSamplerRateBounds(t *testing.T) {
	// Rate 1 keeps everything, Rate 0 keeps nothing.
	hi := sampler.TailSampler{Rate: 1}
	lo := sampler.TailSampler{Rate: 0}
	for i := 0; i < 100; i++ {
		f := map[string]any{"status_code": 200}
		if !hi.ShouldSample(f) {
			t.Fatal("Rate=1 must keep every non-matching event")
		}
		if lo.ShouldSample(f) {
			t.Fatal("Rate=0 must drop every non-matching event")
		}
	}
}

func TestTailSamplerConcurrent(t *testing.T) {
	// Rand nil -> global rand path; must hold under -race.
	s := sampler.NewTailSampler(100, 0.5)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.ShouldSample(map[string]any{"status_code": 200, "duration_ms": 1.0})
		}()
	}
	wg.Wait()
}

func TestTailSamplerCustomRand(t *testing.T) {
	s := sampler.TailSampler{Rate: 1, Rules: []sampler.Rule{func(map[string]any) bool { return false }}}
	if !s.ShouldSample(nil) {
		t.Fatal("rule match should keep the event")
	}
	if !sampler.Always.ShouldSample(nil) || sampler.Never.ShouldSample(nil) {
		t.Fatal("Always/Never built-ins are wrong")
	}
}
