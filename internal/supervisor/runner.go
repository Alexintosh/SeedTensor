// Package supervisor ties Phase 1 together: resolve a model, ensure its files
// are present and verified, launch local inference, and seed the verified files
// while the engine is idle.
package supervisor

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/alessio/seedbox/internal/config"
	"github.com/alessio/seedbox/internal/infer"
	"github.com/alessio/seedbox/internal/model"
	"github.com/alessio/seedbox/internal/platform"
	"github.com/alessio/seedbox/internal/seed"
)

// Fetcher ensures a resolved model's files are present under dataDir. The real
// fetcher drives the torrent helper to download-then-seed; the fixture fetcher
// copies from a local directory. Either way, the Runner verifies afterward, so
// a fetcher is never trusted to produce correct bytes.
type Fetcher interface {
	Fetch(ctx context.Context, r model.Resolved, dataDir string, progress func(done, total int64)) error
}

// Deps are the injectable collaborators, so the Runner is testable without a
// real TensorFold, torrent helper, or host sensors.
type Deps struct {
	Resolver model.Resolver
	Fetcher  Fetcher
	// NewInfer builds the inference supervisor for a verified model dir.
	NewInfer func(cfg config.InferConfig, backend platform.Backend, modelDir, modelID string) InferEngine
	// NewHelper builds the seeding Helper (dials the socket, or a fake).
	NewHelper func(ctx context.Context, cfg config.SeedConfig) (seed.Helper, error)
	// Sensors supplies battery/metered state (idle is wired internally).
	Sensors seed.Sensors
	Logger  *log.Logger
}

// InferEngine is the slice of *infer.Supervisor the Runner needs, as an
// interface for testing.
type InferEngine interface {
	Isolation() platform.NetworkIsolation
	Start(ctx context.Context) error
	WaitHealthy(ctx context.Context) error
	Stop() error
}

// Runner executes a Phase 1 session.
type Runner struct {
	cfg  config.Config
	deps Deps
	log  *log.Logger

	activity *infer.Activity
	ctrl     *seed.Controller
}

// New builds a Runner.
func New(cfg config.Config, deps Deps) *Runner {
	lg := deps.Logger
	if lg == nil {
		lg = log.Default()
	}
	return &Runner{cfg: cfg, deps: deps, log: lg}
}

// Run performs the whole Phase 1 lifecycle and blocks until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) error {
	backend, err := platform.Resolve(r.cfg.Platform)
	if err != nil {
		return err
	}
	r.log.Printf("platform: %s backend", backend)

	// 1. Resolve the model's authenticated metadata.
	resolved, err := r.deps.Resolver.Resolve(r.cfg.Model.ID)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", r.cfg.Model.ID, err)
	}
	r.log.Printf("resolved %s from %s (%d files, %s)", resolved.ID, resolved.Origin,
		len(resolved.Files), humanBytes(resolved.TotalSize()))

	dataDir := r.cfg.Paths.WeightsDir

	// 2. Ensure files present (fetch is a no-op if already complete; verify
	//    decides correctness regardless).
	if r.deps.Fetcher != nil {
		r.log.Printf("fetching into %s", dataDir)
		if err := r.deps.Fetcher.Fetch(ctx, resolved, dataDir, nil); err != nil {
			return fmt.Errorf("fetch: %w", err)
		}
	}

	// 3. Verify every byte against the record. Fail closed.
	r.log.Printf("verifying %s", dataDir)
	vres, err := model.Verify(dataDir, resolved, nil)
	if err != nil {
		for _, p := range vres.Problems {
			r.log.Printf("  verify problem: %s", p)
		}
		return fmt.Errorf("verification failed; refusing to load or seed: %w", err)
	}
	r.log.Printf("verified %d files (%s)", vres.Checked, humanBytes(vres.Bytes))

	// 4. Launch inference.
	engine := r.deps.NewInfer(r.cfg.Infer, backend, dataDir, resolved.ID)
	iso := engine.Isolation()
	r.log.Printf("network isolation: enforceable=%v mechanism=%s — %s",
		iso.Enforceable, iso.Mechanism, iso.Detail)
	if r.cfg.Infer.NoNetwork && !iso.Enforceable {
		return fmt.Errorf("no_network requested but unenforceable on this host; refusing to start so the guarantee is not silently weakened")
	}
	if err := engine.Start(ctx); err != nil {
		return err
	}
	defer engine.Stop()
	r.log.Printf("waiting for inference to become healthy on %s", r.cfg.Infer.ListenAddr)
	if err := engine.WaitHealthy(ctx); err != nil {
		return fmt.Errorf("inference not healthy: %w", err)
	}
	r.log.Printf("inference healthy")

	// 5. Start seeding (unless disabled).
	if r.cfg.Seed.Network == config.SeedOff {
		r.log.Printf("seeding disabled (seed.network=off); serving inference only")
		<-ctx.Done()
		return ctx.Err()
	}

	helper, err := r.deps.NewHelper(ctx, r.cfg.Seed)
	if err != nil {
		return fmt.Errorf("start seed helper: %w", err)
	}
	defer helper.Close(context.Background())

	if err := helper.AddTorrent(ctx, seed.Torrent{
		Magnet:     resolved.Magnet,
		DataDir:    dataDir,
		Network:    string(r.cfg.Seed.Network),
		InfoHashV2: resolved.InfoHashV2,
	}); err != nil {
		return fmt.Errorf("add torrent: %w", err)
	}

	r.activity = infer.NewActivity(r.cfg.Seed.IdleGraceAfterGeneration.D())
	sensors := r.deps.Sensors
	sensors.IdleFunc = r.activity.Idle // seeder yields during generation

	r.ctrl = seed.NewController(
		seed.Policy{
			PauseOnBattery:        r.cfg.Seed.PauseOnBattery,
			PauseOnMetered:        r.cfg.Seed.PauseOnMetered,
			PauseDuringGeneration: r.cfg.Seed.PauseDuringGeneration,
			DailyUploadCapBytes:   r.cfg.Seed.DailyUploadCapBytes,
		},
		helper, sensors, seed.NewLedger(), 5*time.Second,
	)
	r.log.Printf("seeding %s over %s", resolved.ID, r.cfg.Seed.Network)
	return r.ctrl.Run(ctx)
}

// Activity exposes the inference activity tracker so an API shim in front of
// TensorFold can mark generations start/finish. Nil until Run reaches seeding.
func (r *Runner) Activity() *infer.Activity { return r.activity }

// SeedStatus returns the current seeding status, if seeding has started.
func (r *Runner) SeedStatus() (seed.Status, bool) {
	if r.ctrl == nil {
		return seed.Status{}, false
	}
	return r.ctrl.Status(), true
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
