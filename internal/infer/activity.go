package infer

import (
	"sync"
	"sync/atomic"
	"time"
)

// Activity tracks whether the inference engine is currently doing work, so the
// seeder can yield bandwidth during generation. It is safe for concurrent use.
//
// The model is simple: callers mark a generation started and finished (the
// supervisor does this by watching the proxy in front of TensorFold, or by
// polling its metrics). "Idle" means no generation is in flight AND the grace
// period since the last one has elapsed, which avoids flapping between back-to-
// back requests.
type Activity struct {
	mu          sync.Mutex
	inFlight    int
	lastActive  time.Time
	grace       time.Duration
	now         func() time.Time // injectable clock for tests
	generations atomic.Int64     // total generations observed, for status
}

// NewActivity returns an Activity with the given idle grace period.
func NewActivity(grace time.Duration) *Activity {
	return &Activity{grace: grace, now: time.Now}
}

// Start records that a generation began.
func (a *Activity) Start() {
	a.mu.Lock()
	a.inFlight++
	a.mu.Unlock()
	a.generations.Add(1)
}

// Finish records that a generation ended.
func (a *Activity) Finish() {
	a.mu.Lock()
	if a.inFlight > 0 {
		a.inFlight--
	}
	a.lastActive = a.now()
	a.mu.Unlock()
}

// Idle reports whether inference is idle: nothing in flight and the grace
// period since the last generation has elapsed.
func (a *Activity) Idle() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inFlight > 0 {
		return false
	}
	if a.lastActive.IsZero() {
		return true // never generated anything yet
	}
	return a.now().Sub(a.lastActive) >= a.grace
}

// Generations returns the total number of generations observed.
func (a *Activity) Generations() int64 { return a.generations.Load() }

// setClock is used by tests to control time.
func (a *Activity) setClock(f func() time.Time) {
	a.mu.Lock()
	a.now = f
	a.mu.Unlock()
}
