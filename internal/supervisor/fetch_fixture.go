package supervisor

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Alexintosh/SeedTensor/internal/model"
)

// FixtureFetcher copies a model's files from a local source directory into the
// data dir. It exists so the full pipeline runs offline with no torrent client:
// resolve (fixture) → fetch (copy) → verify → serve → seed (fake helper).
//
// It copies only the files named in the record, so an unrelated file in the
// source does not leak into the verified tree.
type FixtureFetcher struct {
	// SourceDir holds the model files to copy from.
	SourceDir string
}

func (f FixtureFetcher) Fetch(ctx context.Context, r model.Resolved, dataDir string, progress func(done, total int64)) error {
	total := r.TotalSize()
	var done int64
	for _, fc := range r.Files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		src := filepath.Join(f.SourceDir, filepath.FromSlash(fc.Path))
		dst := filepath.Join(dataDir, filepath.FromSlash(fc.Path))
		n, err := copyFile(src, dst)
		if err != nil {
			return fmt.Errorf("copy %s: %w", fc.Path, err)
		}
		done += n
		if progress != nil {
			progress(done, total)
		}
	}
	return nil
}

func copyFile(src, dst string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	return io.Copy(out, in)
}
