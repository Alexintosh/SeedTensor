package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Alexintosh/SeedTensor/internal/config"
	"github.com/Alexintosh/SeedTensor/internal/model"
	"github.com/Alexintosh/SeedTensor/internal/platform"
	"github.com/Alexintosh/SeedTensor/internal/seed"
)

// fakeEngine is an InferEngine backed by an httptest server for health checks.
type fakeEngine struct {
	addr    string
	started bool
	iso     platform.NetworkIsolation
}

func (e *fakeEngine) Isolation() platform.NetworkIsolation { return e.iso }
func (e *fakeEngine) Start(context.Context) error          { e.started = true; return nil }
func (e *fakeEngine) WaitHealthy(ctx context.Context) error {
	// The health server is already up (started by the test); just confirm.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+e.addr+"/v1/models", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
func (e *fakeEngine) Stop() error { return nil }

// fakeSeedHelper is a minimal Helper for the e2e test.
type fakeSeedHelper struct {
	added  bool
	paused bool
	closed bool
}

func (h *fakeSeedHelper) AddTorrent(context.Context, seed.Torrent) error { h.added = true; return nil }
func (h *fakeSeedHelper) Pause(context.Context) error                    { h.paused = true; return nil }
func (h *fakeSeedHelper) Resume(context.Context) error                   { h.paused = false; return nil }
func (h *fakeSeedHelper) UploadedBytes(context.Context) (int64, error)   { return 0, nil }
func (h *fakeSeedHelper) Peers(context.Context) (int, error)             { return 0, nil }
func (h *fakeSeedHelper) Close(context.Context) error                    { h.closed = true; return nil }

func TestRunnerEndToEndFixture(t *testing.T) {
	// A model tree in a "source" dir, plus a fixture manifest.
	srcDir := t.TempDir()
	files := map[string][]byte{
		"config.json":       []byte(`{"arch":"test"}`),
		"model.safetensors": []byte("deterministic fake weights"),
	}
	var fcs []model.FileChecksum
	for rel, content := range files {
		full := filepath.Join(srcDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		fcs = append(fcs, model.FileChecksum{Path: rel, Size: int64(len(content)), Algo: model.SHA256, Hex: hex.EncodeToString(sum[:])})
	}
	manifest := model.Resolved{
		ID:     "TensorFold/Test-Model",
		Magnet: "magnet:?xt=urn:btmh:1220beef",
		Files:  fcs,
	}
	manPath := filepath.Join(t.TempDir(), "manifest.json")
	raw, _ := json.MarshalIndent(manifest, "", "  ")
	os.WriteFile(manPath, raw, 0o644)

	// Health server standing in for TensorFold.
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			io.WriteString(w, `{"data":[]}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer hs.Close()
	healthAddr := strings.TrimPrefix(hs.URL, "http://")

	dataDir := t.TempDir()
	cfg := config.Default()
	cfg.Model.ID = "TensorFold/Test-Model"
	cfg.Model.Source = "fixture"
	cfg.Model.FixtureManifest = manPath
	cfg.Paths.WeightsDir = dataDir
	cfg.Infer.ListenAddr = healthAddr
	cfg.Infer.NoNetwork = false
	cfg.Infer.HealthTimeout = config.Duration(3 * time.Second)
	cfg.Seed.Network = config.SeedClearnet

	resolver, err := model.NewResolver(cfg.Model)
	if err != nil {
		t.Fatal(err)
	}

	helper := &fakeSeedHelper{}
	deps := Deps{
		Resolver: resolver,
		Fetcher:  FixtureFetcher{SourceDir: srcDir},
		NewInfer: func(ic config.InferConfig, b platform.Backend, dir, id string) InferEngine {
			return &fakeEngine{addr: healthAddr, iso: platform.NetworkIsolation{Enforceable: true, Mechanism: "test"}}
		},
		NewHelper: func(context.Context, config.SeedConfig) (seed.Helper, error) { return helper, nil },
		Logger:    log.New(io.Discard, "", 0),
	}

	r := New(cfg, deps)
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	err = r.Run(ctx)
	if err != context.DeadlineExceeded {
		t.Fatalf("run returned %v, want deadline exceeded after steady state", err)
	}

	// The model should have been copied and verified into dataDir.
	if _, err := os.Stat(filepath.Join(dataDir, "model.safetensors")); err != nil {
		t.Fatalf("weights not present after fetch: %v", err)
	}
	if !helper.added {
		t.Error("torrent should have been added for seeding")
	}
	st, ok := r.SeedStatus()
	if !ok {
		t.Fatal("seed status should be available")
	}
	if !st.Seeding {
		t.Errorf("should be seeding when idle; status reason=%q", st.Reason)
	}
}

func TestRunnerFailsClosedOnTamper(t *testing.T) {
	srcDir := t.TempDir()
	content := []byte("good weights")
	os.WriteFile(filepath.Join(srcDir, "model.safetensors"), content, 0o644)
	sum := sha256.Sum256(content)

	// Manifest claims a DIFFERENT hash than the source file → verify must fail.
	wrong := strings.Repeat("0", 64)
	manifest := model.Resolved{
		ID:     "TensorFold/Test-Model",
		Magnet: "magnet:?xt=urn:btmh:1220beef",
		Files:  []model.FileChecksum{{Path: "model.safetensors", Size: int64(len(content)), Algo: model.SHA256, Hex: wrong}},
	}
	_ = sum
	manPath := filepath.Join(t.TempDir(), "manifest.json")
	raw, _ := json.Marshal(manifest)
	os.WriteFile(manPath, raw, 0o644)

	dataDir := t.TempDir()
	cfg := config.Default()
	cfg.Model.ID = "TensorFold/Test-Model"
	cfg.Model.Source = "fixture"
	cfg.Model.FixtureManifest = manPath
	cfg.Paths.WeightsDir = dataDir
	cfg.Infer.NoNetwork = false

	resolver, _ := model.NewResolver(cfg.Model)
	engineBuilt := false
	deps := Deps{
		Resolver: resolver,
		Fetcher:  FixtureFetcher{SourceDir: srcDir},
		NewInfer: func(config.InferConfig, platform.Backend, string, string) InferEngine {
			engineBuilt = true
			return &fakeEngine{}
		},
		NewHelper: func(context.Context, config.SeedConfig) (seed.Helper, error) { return &fakeSeedHelper{}, nil },
		Logger:    log.New(io.Discard, "", 0),
	}
	r := New(cfg, deps)
	err := r.Run(context.Background())
	if err == nil {
		t.Fatal("expected verification failure")
	}
	if !strings.Contains(err.Error(), "verification failed") {
		t.Errorf("error = %v, want verification failure", err)
	}
	if engineBuilt {
		t.Error("inference must NOT start when verification fails")
	}
}
