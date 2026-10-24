package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFixtureResolver(t *testing.T) {
	dir := t.TempDir()
	manifest := Resolved{
		ID:     "TensorFold/Ling-3.0-flash-oQ4",
		Magnet: "magnet:?xt=urn:btmh:1220deadbeef",
		Files: []FileChecksum{
			{Path: "model.safetensors", Size: 10, Algo: SHA256, Hex: "aa" + repeat62()},
		},
	}
	raw, _ := json.MarshalIndent(manifest, "", "  ")
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := FixtureResolver{ManifestPath: path}.Resolve("TensorFold/Ling-3.0-flash-oQ4")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.Magnet != manifest.Magnet {
		t.Errorf("magnet = %q", r.Magnet)
	}
	if r.Origin == "" {
		t.Error("origin should be set")
	}
}

func TestFixtureResolverIDMismatch(t *testing.T) {
	dir := t.TempDir()
	raw, _ := json.Marshal(Resolved{
		ID:     "model-a",
		Magnet: "m",
		Files:  []FileChecksum{{Path: "f", Size: 1, Algo: SHA256, Hex: "bb" + repeat62()}},
	})
	path := filepath.Join(dir, "m.json")
	os.WriteFile(path, raw, 0o644)
	if _, err := (FixtureResolver{ManifestPath: path}).Resolve("model-b"); err == nil {
		t.Fatal("expected id mismatch error")
	}
}

// repeat62 returns 62 hex chars so a 2-char prefix completes a 64-char sha256.
func repeat62() string {
	s := ""
	for i := 0; i < 62; i++ {
		s += "0"
	}
	return s
}
