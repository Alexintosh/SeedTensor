package seed

import "context"

// Helper is the BitTorrent engine seedbox drives. The production implementation
// is a separate libtorrent (v2) process spoken to over a local control socket;
// tests use an in-memory fake. Keeping this an interface means the controller,
// gate, and ledger are all testable without a real torrent client.
//
// The contract is deliberately small: seedbox decides policy; the Helper just
// seeds what it's told and reports how much it has uploaded.
type Helper interface {
	// AddTorrent registers a torrent for seeding from an already-present,
	// verified data directory. It must seed the existing files, never
	// re-download or overwrite them.
	AddTorrent(ctx context.Context, t Torrent) error

	// Pause stops uploading without discarding torrent state.
	Pause(ctx context.Context) error

	// Resume continues uploading previously added torrents.
	Resume(ctx context.Context) error

	// UploadedBytes returns total bytes uploaded since the helper started.
	UploadedBytes(ctx context.Context) (int64, error)

	// Peers returns the current connected peer count (for status).
	Peers(ctx context.Context) (int, error)

	// Close shuts the helper down.
	Close(ctx context.Context) error
}

// Torrent describes one seedable model.
type Torrent struct {
	// Magnet is the v2 magnet URI from the resolver.
	Magnet string
	// DataDir is the verified weights directory holding the files. The helper
	// seeds these in place, read-only.
	DataDir string
	// Network selects the overlay: clearnet or i2p. The helper applies the
	// matching tracker/transport. "off" should never reach the helper.
	Network string
	// InfoHashV2 lets the helper confirm it attached to the intended torrent.
	InfoHashV2 string
}
