package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// buildModelTree writes files under a temp dir and returns the dir plus a
// matching, valid Resolved record.
func buildModelTree(t *testing.T, files map[string][]byte) (string, Resolved) {
	t.Helper()
	root := t.TempDir()
	r := Resolved{
		ID:     "TensorFold/Test-Model",
		Magnet: "magnet:?xt=urn:btmh:1220abcd",
		Origin: "fixture:test",
	}
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		r.Files = append(r.Files, FileChecksum{
			Path: rel,
			Size: int64(len(content)),
			Algo: SHA256,
			Hex:  hex.EncodeToString(sum[:]),
		})
	}
	return root, r
}

func TestVerifySuccess(t *testing.T) {
	root, r := buildModelTree(t, map[string][]byte{
		"config.json":          []byte(`{"arch":"test"}`),
		"model.safetensors":    []byte("not real weights, but deterministic"),
		"tokenizer/vocab.json": []byte("{}"),
	})
	res, err := Verify(root, r, nil)
	if err != nil {
		t.Fatalf("verify: %v (%v)", err, res.Problems)
	}
	if !res.OK {
		t.Fatalf("expected OK, got problems: %v", res.Problems)
	}
	if res.Checked != 3 {
		t.Errorf("checked = %d, want 3", res.Checked)
	}
}

func TestVerifyHashMismatch(t *testing.T) {
	root, r := buildModelTree(t, map[string][]byte{
		"model.safetensors": []byte("original"),
	})
	// Corrupt the file on disk after recording its hash.
	if err := os.WriteFile(filepath.Join(root, "model.safetensors"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Same length so it's a pure hash mismatch, not a size mismatch.
	res, err := Verify(root, r, nil)
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("expected ErrVerificationFailed, got %v", err)
	}
	if res.OK {
		t.Fatal("expected not OK")
	}
	if len(res.Problems) != 1 || res.Problems[0].Path != "model.safetensors" {
		t.Fatalf("unexpected problems: %v", res.Problems)
	}
}

func TestVerifyMissingFile(t *testing.T) {
	root, r := buildModelTree(t, map[string][]byte{
		"a.bin": []byte("a"),
		"b.bin": []byte("b"),
	})
	if err := os.Remove(filepath.Join(root, "b.bin")); err != nil {
		t.Fatal(err)
	}
	res, err := Verify(root, r, nil)
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("expected failure, got %v", err)
	}
	found := false
	for _, p := range res.Problems {
		if p.Path == "b.bin" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected missing-file problem for b.bin, got %v", res.Problems)
	}
}

func TestVerifyExtraFile(t *testing.T) {
	root, r := buildModelTree(t, map[string][]byte{
		"a.bin": []byte("a"),
	})
	// An unexpected file in the tree must fail verification.
	if err := os.WriteFile(filepath.Join(root, "surprise.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Verify(root, r, nil)
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("expected failure for extra file, got %v", err)
	}
	found := false
	for _, p := range res.Problems {
		if p.Path == "surprise.bin" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected extra-file problem, got %v", res.Problems)
	}
}

func TestVerifySizeMismatch(t *testing.T) {
	root, r := buildModelTree(t, map[string][]byte{
		"a.bin": []byte("original content"),
	})
	if err := os.WriteFile(filepath.Join(root, "a.bin"), []byte("short"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Verify(root, r, nil)
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("expected failure for size mismatch, got %v", err)
	}
}

func TestVerifyRejectsEmptyRecord(t *testing.T) {
	root := t.TempDir()
	_, err := Verify(root, Resolved{ID: "x", Magnet: "m"}, nil)
	if err == nil {
		t.Fatal("expected error for empty checksum record")
	}
}
