package seed

import (
	"sync"
	"time"
)

// Conditions are the live inputs the gate evaluates each tick.
type Conditions struct {
	InferenceIdle bool
	OnBattery     bool
	Metered       bool
	UploadedToday int64
}

// Policy is the seeding policy derived from config.
type Policy struct {
	PauseOnBattery        bool
	PauseOnMetered        bool
	PauseDuringGeneration bool
	DailyUploadCapBytes   int64 // 0 = unlimited
}

// Decision is the gate's verdict with a human reason for status output.
type Decision struct {
	Seed   bool
	Reason string
}

// Evaluate decides whether seeding should run right now.
func (p Policy) Evaluate(c Conditions) Decision {
	if p.PauseDuringGeneration && !c.InferenceIdle {
		return Decision{false, "paused: inference active"}
	}
	if p.PauseOnBattery && c.OnBattery {
		return Decision{false, "paused: on battery"}
	}
	if p.PauseOnMetered && c.Metered {
		return Decision{false, "paused: metered connection"}
	}
	if p.DailyUploadCapBytes > 0 && c.UploadedToday >= p.DailyUploadCapBytes {
		return Decision{false, "paused: daily upload cap reached"}
	}
	return Decision{true, "seeding"}
}

// Ledger tracks bytes uploaded within a rolling calendar day (local time). It
// persists nothing by itself; the controller is responsible for saving and
// restoring the running total across restarts so the cap can't be bypassed by
// relaunching.
type Ledger struct {
	mu    sync.Mutex
	day   string // YYYY-MM-DD of the current accounting day
	bytes int64
	now   func() time.Time
}

// NewLedger returns an empty ledger.
func NewLedger() *Ledger {
	return &Ledger{now: time.Now}
}

// Add records additional uploaded bytes, rolling over at local midnight.
func (l *Ledger) Add(delta int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rollLocked()
	l.bytes += delta
}

// Today returns bytes uploaded in the current accounting day.
func (l *Ledger) Today() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rollLocked()
	return l.bytes
}

// Restore seeds the ledger from persisted state (day, bytes).
func (l *Ledger) Restore(day string, bytes int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.day = day
	l.bytes = bytes
	l.rollLocked()
}

// Snapshot returns the current day and byte total for persistence.
func (l *Ledger) Snapshot() (string, int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rollLocked()
	return l.day, l.bytes
}

func (l *Ledger) rollLocked() {
	today := l.now().Format("2006-01-02")
	if l.day != today {
		l.day = today
		l.bytes = 0
	}
}

func (l *Ledger) setClock(f func() time.Time) {
	l.mu.Lock()
	l.now = f
	l.mu.Unlock()
}
