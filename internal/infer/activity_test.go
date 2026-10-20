package infer

import (
	"testing"
	"time"
)

func TestActivityIdleLifecycle(t *testing.T) {
	a := NewActivity(30 * time.Second)
	base := time.Unix(1_700_000_000, 0)
	cur := base
	a.setClock(func() time.Time { return cur })

	if !a.Idle() {
		t.Fatal("should be idle before any generation")
	}

	a.Start()
	if a.Idle() {
		t.Fatal("should not be idle during generation")
	}

	a.Finish()
	// Immediately after finishing, still within grace → not idle.
	if a.Idle() {
		t.Fatal("should not be idle within grace period")
	}

	// Advance past grace.
	cur = base.Add(31 * time.Second)
	if !a.Idle() {
		t.Fatal("should be idle after grace elapses")
	}

	if a.Generations() != 1 {
		t.Errorf("generations = %d, want 1", a.Generations())
	}
}

func TestActivityConcurrentInFlight(t *testing.T) {
	a := NewActivity(0)
	a.Start()
	a.Start()
	a.Finish()
	if a.Idle() {
		t.Fatal("still one generation in flight; not idle")
	}
	a.Finish()
	if !a.Idle() {
		t.Fatal("all finished with zero grace; should be idle")
	}
}
