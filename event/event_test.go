package event_test

import (
	"context"
	"testing"

	"github.com/semmidev/logkit/event"
)

func TestSetLastWriteWins(t *testing.T) {
	ev := event.New()
	ev.Set("payment.attempt", 1)
	ev.Set("payment.attempt", 2)

	args := ev.Args()
	for i := 0; i < len(args); i += 2 {
		if args[i] == "payment.attempt" && args[i+1] != 2 {
			t.Fatalf("payment.attempt = %v, want 2", args[i+1])
		}
	}
}

func TestSetErrorLifecycle(t *testing.T) {
	ev := event.New()
	ev.SetError(errTest{"boom"})
	ev.SetError(nil) // clears the error and marks success

	for _, k := range []string{"error", "error_detail"} {
		if _, ok := ev.Snapshot()[k]; ok {
			t.Fatalf("%s should be deleted after SetError(nil)", k)
		}
	}
	if ev.Snapshot()["outcome"] != "success" {
		t.Fatalf("outcome = %v, want success", ev.Snapshot()["outcome"])
	}
}

type errTest struct{ msg string }

func (e errTest) Error() string { return e.msg }

func TestSnapshotExcludesInternalFields(t *testing.T) {
	ev := event.New()
	ev.SetError(errTest{"boom"})
	snap := ev.Snapshot()

	if _, ok := snap["error_detail"]; ok {
		t.Fatal("error_detail must not leak into Snapshot (sampler view)")
	}
	if snap["error"] != "boom" {
		t.Fatalf("error = %v, want boom", snap["error"])
	}
	if _, ok := snap["duration_ms"]; !ok {
		t.Fatal("duration_ms should be computed")
	}
}

func TestArgsEmitsRawErrorNotFlattened(t *testing.T) {
	ev := event.New()
	ev.SetError(errTest{"boom"})

	args := ev.Args()
	for i := 0; i < len(args); i += 2 {
		if args[i] == "error" {
			// must be the raw error value (a LogValuer candidate), not
			// event.Args flattening it to a string itself
			if _, ok := args[i+1].(error); !ok {
				t.Fatalf("Args emitted error as %T, want an error value", args[i+1])
			}
		}
	}
}

func TestEventConcurrentSet(t *testing.T) {
	ev := event.New()
	done := make(chan struct{})
	for i := 0; i < 16; i++ {
		go func(i int) {
			ev.Set("k", i)
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 16; i++ {
		<-done
	}
	if v := ev.Snapshot()["k"]; v == nil {
		t.Fatal("key vanished under concurrent Set")
	}
}

func TestEnrichAndFromContext(t *testing.T) {
	ctx := context.Background()
	event.Enrich(ctx, "ignore", true) // no event on ctx: must not panic

	ev := event.New()
	ctx = event.WithEvent(ctx, ev)
	event.Enrich(ctx, "user.tier", "premium")
	snap := ev.Snapshot()
	if snap["user.tier"] != "premium" {
		t.Fatalf("user.tier = %v, want premium", snap["user.tier"])
	}
}

func TestSetMapMerge(t *testing.T) {
	ev := event.New()
	ev.SetMap(map[string]any{"a": 1, "b": 2})
	// detached map: mutation after SetMap must not affect the event
	m := map[string]any{"b": 99}
	_ = m
	if v := ev.Snapshot(); v["a"] != 1 || v["b"] != 2 {
		t.Fatalf("SetMap merged wrong: %v %v", v["a"], v["b"])
	}
}
