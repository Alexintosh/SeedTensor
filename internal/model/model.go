// Package model resolves a model id to its authenticated distribution metadata
// (a BitTorrent magnet plus a per-file checksum record), and verifies the files
// on disk against that record before the inference engine is allowed to load
// them.
//
// The trust model is deliberately explicit: the swarm is untrusted. We never
// trust bytes because a peer sent them. We trust them only when every file
// matches a checksum from the model's published record, and that record itself
// is pinned to a known source (a PirateFace listing or a local fixture the
// operator controls). A single mismatch fails the whole model closed.
package model

import (
	"fmt"
)

// HashAlgo identifies a checksum algorithm. Phase 1 supports SHA-256, which is
// what PirateFace records against the original Hugging Face files.
type HashAlgo string

const (
	SHA256 HashAlgo = "sha256"
)

// FileChecksum is the expected hash of one file within a model.
type FileChecksum struct {
	// Path is the file path relative to the model root, using forward slashes.
	Path string `json:"path"`
	// Size is the expected size in bytes.
	Size int64 `json:"size"`
	// Algo is the hash algorithm.
	Algo HashAlgo `json:"algo"`
	// Hex is the lowercase hex-encoded expected digest.
	Hex string `json:"hex"`
}

// Resolved is the authenticated distribution metadata for a model: enough to
// fetch it, seed it, and verify it, with a record of where the metadata came
// from so a later audit can retrace the trust chain.
type Resolved struct {
	// ID is the model identifier.
	ID string `json:"id"`

	// Magnet is the BitTorrent magnet URI (v2, so files are individually
	// hashed and swarms can merge across repackagings).
	Magnet string `json:"magnet"`

	// InfoHashV2 is the torrent v2 infohash, if known from the resolver.
	InfoHashV2 string `json:"info_hash_v2,omitempty"`

	// Files is the complete per-file checksum record. Verification requires
	// that the set of files on disk exactly matches this set.
	Files []FileChecksum `json:"files"`

	// Origin records where this metadata was resolved from, verbatim, for
	// audit (e.g. "pirateface:https://pirateface.co/model/..." or
	// "fixture:/path/to/manifest.json").
	Origin string `json:"origin"`

	// Revision pins the upstream revision the checksums correspond to
	// (e.g. a Hugging Face commit), when the resolver provides it.
	Revision string `json:"revision,omitempty"`
}

// TotalSize returns the sum of all file sizes in the record.
func (r Resolved) TotalSize() int64 {
	var n int64
	for _, f := range r.Files {
		n += f.Size
	}
	return n
}

// Validate checks that a Resolved record is internally well-formed before we
// act on it. It does not touch disk or network.
func (r Resolved) Validate() error {
	if r.ID == "" {
		return fmt.Errorf("resolved model has no id")
	}
	if r.Magnet == "" {
		return fmt.Errorf("resolved model %q has no magnet", r.ID)
	}
	if len(r.Files) == 0 {
		return fmt.Errorf("resolved model %q has no file checksums; refusing to trust an empty record", r.ID)
	}
	seen := make(map[string]bool, len(r.Files))
	for i, f := range r.Files {
		if f.Path == "" {
			return fmt.Errorf("file %d has empty path", i)
		}
		if seen[f.Path] {
			return fmt.Errorf("duplicate file path %q in record", f.Path)
		}
		seen[f.Path] = true
		if f.Algo != SHA256 {
			return fmt.Errorf("file %q uses unsupported hash algo %q", f.Path, f.Algo)
		}
		if len(f.Hex) != 64 {
			return fmt.Errorf("file %q has malformed sha256 %q", f.Path, f.Hex)
		}
		if f.Size < 0 {
			return fmt.Errorf("file %q has negative size", f.Path)
		}
	}
	return nil
}

// Resolver turns a model id into authenticated distribution metadata. It must
// NOT download the weights; it only produces the record used to fetch and
// verify them.
type Resolver interface {
	Resolve(id string) (Resolved, error)
}
