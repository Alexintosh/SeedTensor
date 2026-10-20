package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultIsValidExceptModelID(t *testing.T) {
	d := Default()
	if err := d.Validate(); err == nil {
		t.Fatal("default config without model.id should fail validation")
	}
	d.Model.ID = "TensorFold/Qwen3.8-27B-oQ4"
	if err := d.Validate(); err != nil {
		t.Fatalf("default config with model.id should validate: %v", err)
	}
}

func TestLoadPartialFillsDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// A minimal config: only the model id and a comment.
	raw := `{
  // the one model this node serves and seeds
  "model": { "id": "TensorFold/Ling-3.0-flash-oQ4" }
}`
	if err := writeFile(path, raw); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Platform != PlatformAuto {
		t.Errorf("platform = %q, want auto", cfg.Platform)
	}
	if cfg.Infer.ListenAddr != "127.0.0.1:8080" {
		t.Errorf("listen_addr = %q, want default", cfg.Infer.ListenAddr)
	}
	if cfg.Infer.HealthTimeout.D() != 120*time.Second {
		t.Errorf("health_timeout = %v, want 120s", cfg.Infer.HealthTimeout.D())
	}
	if cfg.Seed.Network != SeedClearnet {
		t.Errorf("seed.network = %q, want clearnet", cfg.Seed.Network)
	}
	if !cfg.Seed.PauseDuringGeneration {
		t.Error("pause_during_generation should default true")
	}
	if cfg.Paths.WeightsDir == "" {
		t.Error("weights_dir should be filled from default")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	orig := Default()
	orig.Model.ID = "TensorFold/GLM-5.3-Flash-MLX-4bit-MTP"
	orig.Platform = PlatformMetal
	orig.Seed.DailyUploadCapBytes = 5 << 30 // 5 GiB
	orig.Seed.IdleGraceAfterGeneration = Duration(45 * time.Second)
	if err := Save(path, orig); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Model.ID != orig.Model.ID {
		t.Errorf("model.id = %q, want %q", got.Model.ID, orig.Model.ID)
	}
	if got.Platform != PlatformMetal {
		t.Errorf("platform = %q, want metal", got.Platform)
	}
	if got.Seed.DailyUploadCapBytes != 5<<30 {
		t.Errorf("cap = %d, want %d", got.Seed.DailyUploadCapBytes, 5<<30)
	}
	if got.Seed.IdleGraceAfterGeneration.D() != 45*time.Second {
		t.Errorf("idle grace = %v, want 45s", got.Seed.IdleGraceAfterGeneration.D())
	}
}

func TestRejectsUnknownField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"model": {"id": "x"}, "bogus_field": 1}`
	if err := writeFile(path, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestInvalidEnum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"model": {"id": "x"}, "seed": {"network": "carrier-pigeon"}}`
	if err := writeFile(path, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for invalid seed.network")
	}
}
