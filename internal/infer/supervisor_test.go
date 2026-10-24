package infer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Alexintosh/SeedTensor/internal/config"
	"github.com/Alexintosh/SeedTensor/internal/platform"
)

func baseCfg() config.InferConfig {
	return config.InferConfig{
		ListenAddr:    "127.0.0.1:18080",
		HealthTimeout: config.Duration(3 * time.Second),
		NoNetwork:     false, // tests don't exercise real namespaces
	}
}

func TestSpecBuildsBackendFlags(t *testing.T) {
	s := NewSupervisor(baseCfg(), platform.BackendCUDA, "/weights/x", "TensorFold/Qwen3.8-27B-oQ4")
	spec := s.Spec()
	joined := strings.Join(spec.Args, " ")
	if !strings.Contains(joined, "--backend cuda") {
		t.Errorf("expected cuda backend flag, got %v", spec.Args)
	}
	if !strings.Contains(joined, "TensorFold/Qwen3.8-27B-oQ4") {
		t.Errorf("expected model id in args, got %v", spec.Args)
	}
	if !strings.Contains(joined, "/weights/x") {
		t.Errorf("expected model dir in args, got %v", spec.Args)
	}
}

func TestNoNetworkRequestedButUnenforceableErrors(t *testing.T) {
	cfg := baseCfg()
	cfg.NoNetwork = true
	s := NewSupervisor(cfg, platform.BackendMetal, "/w", "m")
	// Force the unenforceable posture regardless of host OS.
	s.iso = platform.NetworkIsolation{Enforceable: false, Mechanism: "none", Detail: "test"}
	if err := s.Start(context.Background()); err == nil {
		t.Fatal("expected error when no_network is requested but unenforceable")
	}
}

func TestStartAndHealthCheckWithFakeServer(t *testing.T) {
	// A fake TensorFold: an HTTP server answering /v1/models, and a trivial
	// launch that just sleeps so there is a process to supervise.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"data":[{"id":"test"}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := baseCfg()
	// Point health checks at the fake server's address.
	addr := strings.TrimPrefix(srv.URL, "http://")
	cfg.ListenAddr = addr

	s := NewSupervisor(cfg, platform.BackendMetal, "/w", "m")
	s.httpClient = srv.Client()
	// Replace launch with a harmless long-running process.
	s.launch = func(ctx context.Context, spec LaunchSpec) *exec.Cmd {
		return exec.CommandContext(ctx, "sleep", "30")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Stop()

	if err := s.WaitHealthy(ctx); err != nil {
		t.Fatalf("waitHealthy: %v", err)
	}
}

func TestHealthCheckTimesOut(t *testing.T) {
	cfg := baseCfg()
	cfg.ListenAddr = "127.0.0.1:9" // discard-ish; nothing listening
	cfg.HealthTimeout = config.Duration(600 * time.Millisecond)
	s := NewSupervisor(cfg, platform.BackendMetal, "/w", "m")
	s.launch = func(ctx context.Context, spec LaunchSpec) *exec.Cmd {
		return exec.CommandContext(ctx, "sleep", "5")
	}
	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Stop()
	if err := s.WaitHealthy(ctx); err == nil {
		t.Fatal("expected health timeout")
	}
}
