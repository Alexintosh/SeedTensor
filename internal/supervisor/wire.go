package supervisor

import (
	"context"
	"path/filepath"

	"github.com/Alexintosh/SeedTensor/internal/config"
	"github.com/Alexintosh/SeedTensor/internal/infer"
	"github.com/Alexintosh/SeedTensor/internal/model"
	"github.com/Alexintosh/SeedTensor/internal/platform"
	"github.com/Alexintosh/SeedTensor/internal/seed"
)

// Production builds the Deps used in a real run: a resolver from config, the
// real TensorFold supervisor, and a socket-backed seed helper.
//
// The real Fetcher is intentionally nil in Phase 1. The operator places weights
// by running the torrent helper to download-then-seed, or by pointing
// WeightsDir at an existing verified copy. Verify always runs regardless, so
// "no fetcher" means "trust nothing extra, just verify what's on disk". A
// torrent-backed fetcher can be dropped in later without touching the Runner.
func Production(cfg config.Config) (Deps, error) {
	resolver, err := model.NewResolver(cfg.Model)
	if err != nil {
		return Deps{}, err
	}
	return Deps{
		Resolver: resolver,
		Fetcher:  nil,
		NewInfer: func(ic config.InferConfig, b platform.Backend, dir, id string) InferEngine {
			return infer.NewSupervisor(ic, b, dir, id)
		},
		NewHelper: func(ctx context.Context, sc config.SeedConfig) (seed.Helper, error) {
			sock := sc.ControlSocket
			if sock == "" {
				sock = filepath.Join(cfg.Paths.StateDir, "helper.sock")
			}
			return seed.DialSocketHelper(ctx, sock)
		},
		Sensors: hostSensors(),
	}, nil
}
