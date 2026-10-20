package seed

import (
	"context"
	"testing"
	"time"
)

func TestGateDecisions(t *testing.T) {
	p := Policy{
		PauseOnBattery:        true,
		PauseOnMetered:        true,
		PauseDuringGeneration: true,
		DailyUploadCapBytes:   1000,
	}
	cases := []struct {
		name string
		cond Conditions
		want bool
	}{
		{"all clear", Conditions{InferenceIdle: true}, true},
		{"generating", Conditions{InferenceIdle: false}, false},
		{"battery", Conditions{InferenceIdle: true, OnBattery: true}, false},
		{"metered", Conditions{InferenceIdle: true, Metered: true}, false},
		{"cap reached", Conditions{InferenceIdle: true, UploadedToday: 1000}, false},
		{"under cap", Conditions{InferenceIdle: true, UploadedToday: 999}, true},
	}
	for _, tc := range cases {
		if got := p.Evaluate(tc.cond).Seed; got != tc.want {
			t.Errorf("%s: seed=%v want %v", tc.name, got, tc.want)
		}
	}
}

func TestGatePriorityGenerationOverCap(t *testing.T) {
	// Generation pause should be reported even if the cap is also hit; the
	// first failing condition wins, and generation is checked first.
	p := Policy{PauseDuringGeneration: true, DailyUploadCapBytes: 10}
	d := p.Evaluate(Conditions{InferenceIdle: false, UploadedToday: 100})
	if d.Seed {
		t.Fatal("should not seed")
	}
	if d.Reason != "paused: inference active" {
		t.Errorf("reason = %q", d.Reason)
	}
}

func TestLedgerRollover(t *testing.T) {
	l := NewLedger()
	base := time.Date(2026, 10, 20, 23, 59, 0, 0, time.Local)
	cur := base
	l.setClock(func() time.Time { return cur })

	l.Add(500)
	if l.Today() != 500 {
		t.Fatalf("today = %d, want 500", l.Today())
	}
	// Cross midnight.
	cur = base.Add(2 * time.Minute)
	if l.Today() != 0 {
		t.Fatalf("after midnight today = %d, want 0", l.Today())
	}
	l.Add(10)
	if l.Today() != 10 {
		t.Fatalf("today = %d, want 10", l.Today())
	}
}

func TestControllerResumesWhenIdle(t *testing.T) {
	h := &fakeHelper{paused: true}
	ctrl := NewController(
		Policy{PauseDuringGeneration: true},
		h,
		Sensors{IdleFunc: func() bool { return true }},
		NewLedger(),
		time.Hour, // tick won't fire; we call step directly
	)
	ctrl.step(context.Background())
	if h.isPaused() {
		t.Fatal("helper should be resumed when idle and no other constraint")
	}
	if !ctrl.Status().Seeding {
		t.Fatal("controller status should report seeding")
	}
}

func TestControllerPausesDuringGeneration(t *testing.T) {
	h := &fakeHelper{paused: false}
	idle := false
	ctrl := NewController(
		Policy{PauseDuringGeneration: true},
		h,
		Sensors{IdleFunc: func() bool { return idle }},
		NewLedger(),
		time.Hour,
	)
	ctrl.step(context.Background())
	if !h.isPaused() {
		t.Fatal("helper should be paused during generation")
	}
	// Now go idle.
	idle = true
	ctrl.step(context.Background())
	if h.isPaused() {
		t.Fatal("helper should resume once idle")
	}
}

func TestControllerEnforcesCapFromUploadCounter(t *testing.T) {
	h := &fakeHelper{paused: false}
	ctrl := NewController(
		Policy{DailyUploadCapBytes: 1000},
		h,
		Sensors{IdleFunc: func() bool { return true }},
		NewLedger(),
		time.Hour,
	)
	// Under cap: seeds.
	h.setUploaded(500)
	ctrl.step(context.Background())
	if h.isPaused() {
		t.Fatal("should seed under cap")
	}
	// Cross the cap: the controller folds the counter into the ledger and
	// pauses.
	h.setUploaded(1000)
	ctrl.step(context.Background())
	if !h.isPaused() {
		t.Fatal("should pause once cap reached")
	}
	if ctrl.Status().UploadedToday < 1000 {
		t.Errorf("ledger should reflect >=1000, got %d", ctrl.Status().UploadedToday)
	}
}

func TestControllerRunStopsOnContext(t *testing.T) {
	h := &fakeHelper{}
	ctrl := NewController(Policy{}, h, Sensors{IdleFunc: func() bool { return true }}, NewLedger(), 10*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := ctrl.Run(ctx)
	if err == nil {
		t.Fatal("expected context error on stop")
	}
	if !h.isPaused() {
		t.Fatal("helper should be paused on shutdown")
	}
}
