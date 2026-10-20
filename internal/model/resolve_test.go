package model

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestPirateFaceResolverHTTP(t *testing.T) {
	// A fake PirateFace serving the assumed schema.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/v1/model/TensorFold%2FQwen3.8-27B-oQ4" &&
			req.URL.Path != "/api/v1/model/TensorFold/Qwen3.8-27B-oQ4" {
			http.Error(w, "not found: "+req.URL.Path, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id": "TensorFold/Qwen3.8-27B-oQ4",
			"magnet": "magnet:?xt=urn:btmh:1220feedface&dn=qwen",
			"info_hash_v2": "feedface",
			"revision": "abc123",
			"files": [
				{"path": "model.safetensors", "size": 1024, "sha256": "` + "AA" + repeat62() + `"},
				{"path": "config.json", "size": 64, "sha256": "` + "bb" + repeat62() + `"}
			]
		}`))
	}))
	defer srv.Close()

	r, err := PirateFaceResolver{Base: srv.URL, HTTP: srv.Client()}.Resolve("TensorFold/Qwen3.8-27B-oQ4")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(r.Files) != 2 {
		t.Fatalf("files = %d, want 2", len(r.Files))
	}
	// SHA should be lowercased.
	if r.Files[0].Hex[:2] != "aa" {
		t.Errorf("sha not lowercased: %q", r.Files[0].Hex)
	}
	if r.InfoHashV2 != "feedface" {
		t.Errorf("infohash = %q", r.InfoHashV2)
	}
}

func TestPirateFaceResolver404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no such model", http.StatusNotFound)
	}))
	defer srv.Close()
	if _, err := (PirateFaceResolver{Base: srv.URL, HTTP: srv.Client()}).Resolve("nope"); err == nil {
		t.Fatal("expected error on 404")
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
