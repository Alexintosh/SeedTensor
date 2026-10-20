package seed

import (
	"context"
	"sync"
	"time"
)

// Sensors supplies the live host conditions the gate needs. Real
// implementations read battery and connection state from the OS; tests inject
// fakes. IdleFunc reports whether inference is idle (wired to infer.Activity).
type Sensors struct {
	IdleFunc    func() bool
	BatteryFunc func() bool // true = on battery
	MeteredFunc func() bool // true = metered connection
}

func (s Sensors) idle() bool {
	if s.IdleFunc == nil {
		return true
	}
	return s.IdleFunc()
}
func (s Sensors) battery() bool {
	if s.BatteryFunc == nil {
		return false
	}
	return s.BatteryFunc()
}
func (s Sensors) metered() bool {
	if s.MeteredFunc == nil {
		return false
	}
	return s.MeteredFunc()
}

// Controller applies the seeding policy to the Helper on a fixed tick. It owns
// the ledger and the pause/resume state so the Helper only ever reflects the
// gate's current decision.
type Controller struct {
	policy  Policy
	helper  Helper
	sensors Sensors
	ledger  *Ledger
	tick    time.Duration

	mu           sync.Mutex
	seeding      bool
	initialized  bool // whether we've driven the helper to a known state yet
	lastUploaded int64
	lastDecision Decision
}

// NewController builds a controller. tick <= 0 defaults to 5s.
func NewController(policy Policy, helper Helper, sensors Sensors, ledger *Ledger, tick time.Duration) *Controller {
	if tick <= 0 {
		tick = 5 * time.Second
	}
	if ledger == nil {
		ledger = NewLedger()
	}
	return &Controller{
		policy:  policy,
		helper:  helper,
		sensors: sensors,
		ledger:  ledger,
		tick:    tick,
	}
}

// Run drives the control loop until ctx is cancelled. It evaluates the gate
// each tick, updates the upload ledger from the helper's counter, and
// pauses/resumes the helper to match the decision.
func (c *Controller) Run(ctx context.Context) error {
	t := time.NewTicker(c.tick)
	defer t.Stop()

	// Evaluate once immediately so startup state is correct without waiting
	// a full tick.
	c.step(ctx)
	for {
		select {
		case <-ctx.Done():
			// Best-effort pause on shutdown.
			pctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = c.helper.Pause(pctx)
			cancel()
			return ctx.Err()
		case <-t.C:
			c.step(ctx)
		}
	}
}

// step performs one evaluation cycle. Exposed (unexported) for direct testing.
func (c *Controller) step(ctx context.Context) {
	// Fold the helper's cumulative upload counter into the daily ledger.
	if up, err := c.helper.UploadedBytes(ctx); err == nil {
		c.mu.Lock()
		if up >= c.lastUploaded {
			c.ledger.Add(up - c.lastUploaded)
		} else {
			// Counter reset (helper restarted); count the new value as-is.
			c.ledger.Add(up)
		}
		c.lastUploaded = up
		c.mu.Unlock()
	}

	cond := Conditions{
		InferenceIdle: c.sensors.idle(),
		OnBattery:     c.sensors.battery(),
		Metered:       c.sensors.metered(),
		UploadedToday: c.ledger.Today(),
	}
	decision := c.policy.Evaluate(cond)

	c.mu.Lock()
	c.lastDecision = decision
	want := decision.Seed
	have := c.seeding
	init := c.initialized
	c.initialized = true
	c.mu.Unlock()

	// On the first step the helper's real state is unknown, so always drive
	// it explicitly rather than assuming it already matches `have`.
	if init && want == have {
		return
	}
	if want {
		if err := c.helper.Resume(ctx); err == nil {
			c.setSeeding(true)
		}
	} else {
		if err := c.helper.Pause(ctx); err == nil {
			c.setSeeding(false)
		}
	}
}

func (c *Controller) setSeeding(v bool) {
	c.mu.Lock()
	c.seeding = v
	c.mu.Unlock()
}

// Status is a snapshot for the status command.
type Status struct {
	Seeding       bool
	Reason        string
	UploadedToday int64
}

// Status returns the current controller state.
func (c *Controller) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Status{
		Seeding:       c.seeding,
		Reason:        c.lastDecision.Reason,
		UploadedToday: c.ledger.Today(),
	}
}

// Ledger exposes the ledger for persistence by the supervisor.
func (c *Controller) Ledger() *Ledger { return c.ledger }
