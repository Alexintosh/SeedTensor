package seed

import (
	"context"
	"sync"
)

// fakeHelper is an in-memory Helper for tests. It records calls and lets the
// test drive the uploaded-bytes counter.
type fakeHelper struct {
	mu       sync.Mutex
	added    []Torrent
	paused   bool
	uploaded int64
	peers    int
	closed   bool
}

func (f *fakeHelper) AddTorrent(_ context.Context, t Torrent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.added = append(f.added, t)
	return nil
}

func (f *fakeHelper) Pause(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused = true
	return nil
}

func (f *fakeHelper) Resume(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused = false
	return nil
}

func (f *fakeHelper) UploadedBytes(_ context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.uploaded, nil
}

func (f *fakeHelper) Peers(_ context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.peers, nil
}

func (f *fakeHelper) Close(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeHelper) setUploaded(n int64) {
	f.mu.Lock()
	f.uploaded = n
	f.mu.Unlock()
}

func (f *fakeHelper) isPaused() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.paused
}
