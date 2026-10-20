package model

import (
	"fmt"

	"github.com/alessio/seedbox/internal/config"
)

// NewResolver builds the Resolver named by the model config.
func NewResolver(mc config.ModelConfig) (Resolver, error) {
	switch mc.Source {
	case "pirateface":
		return PirateFaceResolver{Base: mc.PirateFaceBase}, nil
	case "fixture":
		return FixtureResolver{ManifestPath: mc.FixtureManifest}, nil
	default:
		return nil, fmt.Errorf("unknown model source %q", mc.Source)
	}
}
