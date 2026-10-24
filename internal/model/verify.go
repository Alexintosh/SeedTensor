package model

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// VerifyResult is the outcome of verifying a model directory against its
// checksum record.
type VerifyResult struct {
	OK       bool
	Checked  int   // files hashed
	Bytes    int64 // total bytes hashed
	Problems []Problem
}

// Problem describes one verification failure.
type Problem struct {
	Path   string
	Reason string
}

func (p Problem) String() string { return fmt.Sprintf("%s: %s", p.Path, p.Reason) }

// ErrVerificationFailed is returned by Verify when the directory does not match
// the record. The VerifyResult carries the specifics.
var ErrVerificationFailed = errors.New("model verification failed")

// Verify hashes every file under root and checks it against r. It fails closed:
// any missing file, extra file, size mismatch, or hash mismatch makes the whole
// model invalid. On success every byte the inference engine will load has been
// confirmed to match the authenticated record.
//
// progress, if non-nil, is called periodically with (bytesHashed, totalBytes).
func Verify(root string, r Resolved, progress func(done, total int64)) (VerifyResult, error) {
	if err := r.Validate(); err != nil {
		return VerifyResult{}, fmt.Errorf("record invalid: %w", err)
	}

	res := VerifyResult{OK: true}
	total := r.TotalSize()

	// Index expected files for lookup and extra-file detection.
	expected := make(map[string]FileChecksum, len(r.Files))
	for _, f := range r.Files {
		expected[filepath.FromSlash(f.Path)] = f
	}

	// Walk the directory to find any files not in the record. An unexpected
	// file is a verification failure: we will not load or seed a tree that
	// carries bytes the record does not account for.
	onDisk := make(map[string]bool)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		onDisk[rel] = true
		if _, ok := expected[rel]; !ok {
			res.OK = false
			res.Problems = append(res.Problems, Problem{
				Path:   filepath.ToSlash(rel),
				Reason: "unexpected file not present in checksum record",
			})
		}
		return nil
	})
	if err != nil {
		return VerifyResult{}, fmt.Errorf("walk %s: %w", root, err)
	}

	// Hash each expected file in a stable order so progress is monotonic.
	paths := make([]string, 0, len(expected))
	for p := range expected {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var done int64
	for _, rel := range paths {
		fc := expected[rel]
		full := filepath.Join(root, rel)

		if !onDisk[rel] {
			res.OK = false
			res.Problems = append(res.Problems, Problem{Path: fc.Path, Reason: "file missing from disk"})
			continue
		}

		info, err := os.Stat(full)
		if err != nil {
			res.OK = false
			res.Problems = append(res.Problems, Problem{Path: fc.Path, Reason: fmt.Sprintf("stat: %v", err)})
			continue
		}
		if info.Size() != fc.Size {
			res.OK = false
			res.Problems = append(res.Problems, Problem{
				Path:   fc.Path,
				Reason: fmt.Sprintf("size mismatch: have %d, want %d", info.Size(), fc.Size),
			})
			// Still hash below would be wasteful; skip to next file.
			done += info.Size()
			if progress != nil {
				progress(done, total)
			}
			continue
		}

		sum, n, err := hashFile(full, fc.Algo, fc.Size, func(delta int64) {
			if progress != nil {
				progress(done+delta, total)
			}
		})
		if err != nil {
			res.OK = false
			res.Problems = append(res.Problems, Problem{Path: fc.Path, Reason: fmt.Sprintf("read: %v", err)})
			done += fc.Size
			continue
		}
		done += n
		res.Checked++
		res.Bytes += n

		if !strings.EqualFold(sum, fc.Hex) {
			res.OK = false
			res.Problems = append(res.Problems, Problem{
				Path:   fc.Path,
				Reason: fmt.Sprintf("%s mismatch: have %s, want %s", fc.Algo, sum, fc.Hex),
			})
		}
	}

	if !res.OK {
		return res, ErrVerificationFailed
	}
	return res, nil
}

// hashFile streams a file through the record's algorithm without loading it
// into memory, which matters because model files run to hundreds of gigabytes.
// For GitBlobSHA1 the git object header "blob <size>\x00" is hashed first,
// matching `git hash-object` and Hugging Face's non-LFS file oid. size is the
// already-checked file size.
func hashFile(path string, algo HashAlgo, size int64, onRead func(delta int64)) (hexsum string, n int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	var h hash.Hash
	switch algo {
	case SHA256:
		h = sha256.New()
	case GitBlobSHA1:
		h = sha1.New()
		fmt.Fprintf(h, "blob %d\x00", size)
	default:
		return "", 0, fmt.Errorf("unsupported hash algo %q", algo)
	}
	buf := make([]byte, 4<<20) // 4 MiB
	for {
		r, rerr := f.Read(buf)
		if r > 0 {
			h.Write(buf[:r])
			n += int64(r)
			if onRead != nil {
				onRead(int64(r))
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", n, rerr
		}
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
