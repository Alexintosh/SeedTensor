package model

import (
	"encoding/json"
	"fmt"
	"os"
)

// FixtureResolver resolves a model from a local JSON manifest. It exists so the
// whole pipeline (resolve, fetch, verify, seed) can run offline against a
// controlled fixture, with no dependency on a live registry or network. The
// manifest format is exactly the Resolved struct serialized to JSON.
type FixtureResolver struct {
	ManifestPath string
}

func (f FixtureResolver) Resolve(id string) (Resolved, error) {
	raw, err := os.ReadFile(f.ManifestPath)
	if err != nil {
		return Resolved{}, fmt.Errorf("read fixture manifest: %w", err)
	}
	var r Resolved
	if err := json.Unmarshal(raw, &r); err != nil {
		return Resolved{}, fmt.Errorf("parse fixture manifest: %w", err)
	}
	if r.ID == "" {
		r.ID = id
	}
	if r.ID != id {
		return Resolved{}, fmt.Errorf("fixture manifest is for %q, requested %q", r.ID, id)
	}
	if r.Origin == "" {
		r.Origin = "fixture:" + f.ManifestPath
	}
	if err := r.Validate(); err != nil {
		return Resolved{}, err
	}
	return r, nil
}
