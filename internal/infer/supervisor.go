package infer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/alessio/seedbox/internal/config"
	"github.com/alessio/seedbox/internal/platform"
)

// Supervisor launches and monitors the TensorFold inference server.
type Supervisor struct {
	cfg      config.InferConfig
	backend  platform.Backend
	modelDir string
	modelID  string
	iso      platform.NetworkIsolation

	// launch builds the *exec.Cmd to run. Overridable in tests.
	launch func(ctx context.Context, spec LaunchSpec) *exec.Cmd

	// httpClient checks health. Overridable in tests.
	httpClient *http.Client

	cmd *exec.Cmd
}

// LaunchSpec is the resolved description of how to start TensorFold, passed to
// the launch function so tests can inspect it.
type LaunchSpec struct {
	Binary     string
	Args       []string
	Env        []string
	Isolation  platform.NetworkIsolation
	ModelDir   string
	ListenAddr string
}

// NewSupervisor builds an inference supervisor. modelDir is the verified
// weights directory; modelID is the TensorFold model id to serve.
func NewSupervisor(cfg config.InferConfig, backend platform.Backend, modelDir, modelID string) *Supervisor {
	return &Supervisor{
		cfg:        cfg,
		backend:    backend,
		modelDir:   modelDir,
		modelID:    modelID,
		iso:        platform.DetectNetworkIsolation(),
		launch:     defaultLaunch,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// Isolation reports the network-isolation posture that will be applied.
func (s *Supervisor) Isolation() platform.NetworkIsolation { return s.iso }

// Spec builds the LaunchSpec without starting anything. Exposed for status
// output and tests.
func (s *Supervisor) Spec() LaunchSpec {
	bin := s.cfg.Binary
	if bin == "" {
		bin = "tensorfold"
	}
	// TensorFold serves an OpenAI-compatible API; the model dir is the
	// verified weights. The exact flag surface is TensorFold's; these are
	// the documented serve knobs (serve <repo>, --host/--port via listen).
	args := []string{
		"serve", s.modelID,
		"--model-dir", s.modelDir,
		"--listen", s.cfg.ListenAddr,
	}
	switch s.backend {
	case platform.BackendCUDA:
		args = append(args, "--backend", "cuda")
	case platform.BackendMetal:
		args = append(args, "--backend", "metal")
	}
	return LaunchSpec{
		Binary:     bin,
		Args:       args,
		Env:        os.Environ(),
		Isolation:  s.iso,
		ModelDir:   s.modelDir,
		ListenAddr: s.cfg.ListenAddr,
	}
}

// Start launches TensorFold. If config requests no-network and the platform
// cannot enforce it, Start returns an error unless the caller has accepted the
// gap (handled one level up). Here, we honor the request strictly: an
// unenforceable no-network request is an error, so the guarantee is never
// silently downgraded.
func (s *Supervisor) Start(ctx context.Context) error {
	if s.cfg.NoNetwork && !s.iso.Enforceable {
		return fmt.Errorf("no_network requested but not enforceable on this host: %s", s.iso.Detail)
	}
	spec := s.Spec()
	s.cmd = s.launch(ctx, spec)
	if s.cmd == nil {
		return errors.New("launch produced no command")
	}
	if err := s.cmd.Start(); err != nil {
		return fmt.Errorf("start tensorfold: %w", err)
	}
	return nil
}

// WaitHealthy polls the OpenAI-compatible /v1/models endpoint until it answers
// or the health timeout elapses.
func (s *Supervisor) WaitHealthy(ctx context.Context) error {
	deadline := time.Now().Add(s.cfg.HealthTimeout.D())
	url := "http://" + s.cfg.ListenAddr + "/v1/models"
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("tensorfold did not become healthy within %s", s.cfg.HealthTimeout.D())
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := s.httpClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// Stop terminates the inference process if running.
func (s *Supervisor) Stop() error {
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	return s.cmd.Process.Kill()
}

// Wait blocks until the process exits.
func (s *Supervisor) Wait() error {
	if s.cmd == nil {
		return nil
	}
	return s.cmd.Wait()
}

// defaultLaunch builds the real command, applying Linux network-namespace
// isolation when requested and available. On Linux we wrap with `unshare -rn`
// (new user+net namespaces, unprivileged) then bring up loopback so the local
// server still binds 127.0.0.1 while every external interface is gone.
func defaultLaunch(ctx context.Context, spec LaunchSpec) *exec.Cmd {
	if spec.Isolation.Enforceable && spec.Isolation.Mechanism == "linux-netns" {
		// unshare -r -n: new user + network namespace. Then `ip link set lo up`
		// via a shell so loopback works, then exec the server.
		inner := shellQuote(append([]string{spec.Binary}, spec.Args...))
		script := "ip link set lo up 2>/dev/null; exec " + inner
		cmd := exec.CommandContext(ctx, "unshare", "-r", "-n", "sh", "-c", script)
		cmd.Env = spec.Env
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd
	}
	cmd := exec.CommandContext(ctx, spec.Binary, spec.Args...)
	cmd.Env = spec.Env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

// shellQuote joins args into a single-quoted shell string.
func shellQuote(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += "'" + escapeSingle(a) + "'"
	}
	return out
}

func escapeSingle(s string) string {
	// Replace ' with '\'' .
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'', '\\', '\'', '\'')
			continue
		}
		out = append(out, s[i])
	}
	return string(out)
}
