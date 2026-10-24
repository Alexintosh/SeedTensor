// Package config defines the on-disk configuration for seedtensor and the
// defaults that apply when fields are omitted.
//
// Phase 1 scope: this config describes one model served locally by TensorFold
// and seeded over BitTorrent while the inference engine is idle. It deliberately
// carries no remote-inference, payment, or harness settings; those belong to
// later phases.
//
// Config is JSON on disk, parsed with the standard library so the project has
// no third-party dependencies and builds on any machine without network
// access to a module proxy. Durations are written as Go duration strings
// (e.g. "120s", "30s") via the Duration wrapper below.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Duration wraps time.Duration so it marshals to/from a human string in JSON
// (e.g. "30s") rather than an integer nanosecond count.
type Duration time.Duration

func (d Duration) D() time.Duration { return time.Duration(d) }

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch x := v.(type) {
	case string:
		parsed, err := time.ParseDuration(x)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", x, err)
		}
		*d = Duration(parsed)
	case float64:
		// Accept a bare number as nanoseconds for convenience.
		*d = Duration(time.Duration(x))
	default:
		return fmt.Errorf("duration must be a string like \"30s\", got %T", v)
	}
	return nil
}

// Platform selects the TensorFold serve path. "auto" resolves at runtime.
type Platform string

const (
	PlatformAuto  Platform = "auto"
	PlatformMetal Platform = "metal" // Apple Silicon
	PlatformCUDA  Platform = "cuda"  // NVIDIA
)

// SeedNetwork selects how the seeder reaches peers.
//
// For Phase 1 local-only inference, the only thing seeding reveals is that an
// IP holds a given model, so clearnet is an acceptable default. I2P is offered
// for users who do not want their address in the public swarm; "off" disables
// seeding entirely (fetch-and-verify still runs).
type SeedNetwork string

const (
	SeedClearnet SeedNetwork = "clearnet"
	SeedI2P      SeedNetwork = "i2p"
	SeedOff      SeedNetwork = "off"
)

// Config is the full seedtensor configuration.
type Config struct {
	// Platform for the inference engine. Defaults to auto.
	Platform Platform `json:"platform"`

	// Model names the single model this instance serves and seeds.
	Model ModelConfig `json:"model"`

	// Infer configures the TensorFold inference process.
	Infer InferConfig `json:"infer"`

	// Seed configures the BitTorrent seeder.
	Seed SeedConfig `json:"seed"`

	// Paths hold on-disk locations. Empty fields are filled from defaults
	// rooted at the OS user cache/config dirs.
	Paths PathsConfig `json:"paths"`
}

// ModelConfig identifies the model and where its authenticated metadata lives.
type ModelConfig struct {
	// ID is the model identifier, e.g. "TensorFold/Qwen3.8-27B-oQ4".
	ID string `json:"id"`

	// Source selects how the model's magnet + checksum record is resolved.
	// "pirateface" uses the PirateFace registry; "fixture" reads a local
	// manifest (used for offline development and tests).
	Source string `json:"source"`

	// FixtureManifest is the path to a local manifest when Source == "fixture".
	FixtureManifest string `json:"fixture_manifest,omitempty"`

	// PirateFaceBase overrides the PirateFace base URL. Empty uses the default.
	PirateFaceBase string `json:"pirateface_base,omitempty"`
}

// InferConfig configures the TensorFold process.
type InferConfig struct {
	// Binary is the TensorFold executable. Empty means "tensorfold" on PATH.
	Binary string `json:"binary,omitempty"`

	// ListenAddr is the loopback address for the OpenAI-compatible server.
	ListenAddr string `json:"listen_addr"`

	// HealthTimeout bounds how long we wait for the server to answer /v1/models.
	HealthTimeout Duration `json:"health_timeout"`

	// NoNetwork, when true, launches inference inside a sandbox with no
	// network namespace (Linux). On platforms where that is unavailable the
	// supervisor records that the guarantee could not be enforced.
	NoNetwork bool `json:"no_network"`
}

// SeedConfig configures the seeder.
type SeedConfig struct {
	Network SeedNetwork `json:"network"`

	// DailyUploadCapBytes caps bytes uploaded per rolling 24h. 0 means unlimited.
	DailyUploadCapBytes int64 `json:"daily_upload_cap_bytes"`

	// PauseOnBattery pauses seeding while the host runs on battery.
	PauseOnBattery bool `json:"pause_on_battery"`

	// PauseOnMetered pauses seeding on a connection the OS reports as metered.
	PauseOnMetered bool `json:"pause_on_metered"`

	// PauseDuringGeneration pauses seeding while the model is actively
	// generating, so uploads never contend with inference latency.
	PauseDuringGeneration bool `json:"pause_during_generation"`

	// IdleGraceAfterGeneration is how long after the last generation finishes
	// before seeding is considered idle-eligible again.
	IdleGraceAfterGeneration Duration `json:"idle_grace_after_generation"`

	// HelperBinary is the libtorrent helper. Empty means "seedtensor-lt-helper"
	// on PATH. The helper is a separate process driven over a control socket.
	HelperBinary string `json:"helper_binary,omitempty"`

	// ControlSocket is the unix socket the Go supervisor uses to drive the
	// helper. Empty uses a default under the runtime dir.
	ControlSocket string `json:"control_socket,omitempty"`
}

// PathsConfig holds on-disk locations.
type PathsConfig struct {
	// WeightsDir is where verified model files live. The inference engine
	// reads them; the seeder shares them read-only. One copy, no duplication.
	WeightsDir string `json:"weights_dir"`

	// StateDir holds seedtensor runtime state (upload ledger, resume data).
	StateDir string `json:"state_dir"`
}

// Default returns a Config with every optional field populated.
func Default() Config {
	cacheDir, _ := os.UserCacheDir()
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), "seedtensor-cache")
	}
	root := filepath.Join(cacheDir, "seedtensor")
	return Config{
		Platform: PlatformAuto,
		Model: ModelConfig{
			Source: "pirateface",
		},
		Infer: InferConfig{
			ListenAddr:    "127.0.0.1:8080",
			HealthTimeout: Duration(120 * time.Second),
			NoNetwork:     true,
		},
		Seed: SeedConfig{
			Network:                  SeedClearnet,
			DailyUploadCapBytes:      0,
			PauseOnBattery:           true,
			PauseOnMetered:           true,
			PauseDuringGeneration:    true,
			IdleGraceAfterGeneration: Duration(30 * time.Second),
		},
		Paths: PathsConfig{
			WeightsDir: filepath.Join(root, "weights"),
			StateDir:   filepath.Join(root, "state"),
		},
	}
}

// Load reads a JSON config from path and fills defaults for omitted fields.
func Load(path string) (Config, error) {
	cfg := Default()
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	// Decode over the defaults so omitted fields keep their default values.
	dec := json.NewDecoder(newCommentStripReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	cfg.fillDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Save writes cfg to path as indented JSON (used by `seedtensor init`).
func Save(path string, cfg Config) error {
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// fillDefaults repairs any field left zero by a partial YAML file.
func (c *Config) fillDefaults() {
	d := Default()
	if c.Platform == "" {
		c.Platform = d.Platform
	}
	if c.Model.Source == "" {
		c.Model.Source = d.Model.Source
	}
	if c.Infer.ListenAddr == "" {
		c.Infer.ListenAddr = d.Infer.ListenAddr
	}
	if c.Infer.HealthTimeout == 0 {
		c.Infer.HealthTimeout = d.Infer.HealthTimeout
	}
	if c.Seed.Network == "" {
		c.Seed.Network = d.Seed.Network
	}
	if c.Seed.IdleGraceAfterGeneration == 0 {
		c.Seed.IdleGraceAfterGeneration = d.Seed.IdleGraceAfterGeneration
	}
	if c.Paths.WeightsDir == "" {
		c.Paths.WeightsDir = d.Paths.WeightsDir
	}
	if c.Paths.StateDir == "" {
		c.Paths.StateDir = d.Paths.StateDir
	}
}

// Validate checks required fields and enum values.
func (c *Config) Validate() error {
	if c.Model.ID == "" {
		return errors.New("model.id is required")
	}
	switch c.Platform {
	case PlatformAuto, PlatformMetal, PlatformCUDA:
	default:
		return fmt.Errorf("invalid platform %q", c.Platform)
	}
	switch c.Model.Source {
	case "pirateface":
	case "fixture":
		if c.Model.FixtureManifest == "" {
			return errors.New("model.fixture_manifest is required when source is fixture")
		}
	default:
		return fmt.Errorf("invalid model.source %q", c.Model.Source)
	}
	switch c.Seed.Network {
	case SeedClearnet, SeedI2P, SeedOff:
	default:
		return fmt.Errorf("invalid seed.network %q", c.Seed.Network)
	}
	if c.Seed.DailyUploadCapBytes < 0 {
		return errors.New("seed.daily_upload_cap_bytes must be >= 0")
	}
	return nil
}
