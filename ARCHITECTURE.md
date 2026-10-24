# Architecture (Phase 1)

```
                         seedtensor run
                              │
            ┌─────────────────┼──────────────────┐
            │                 │                  │
      resolve model      verify weights      launch + seed
            │                 │                  │
   model.Resolver      model.Verify        infer.Supervisor
   (PirateFace |       (SHA-256, fail-     (TensorFold, no-net
    fixture)           closed)             namespace on Linux)
            │                 │                  │
            └──────── supervisor.Runner ─────────┘
                              │
                     seed.Controller ──drives── seed.Helper
                     (gate: idle/battery/        (libtorrent v2
                      metered/cap)                helper process)
```

## Trust model

The swarm is untrusted. seedtensor never trusts bytes because a peer sent them. It
trusts them only when every file matches Hugging Face's fingerprint for that
file at a pinned revision, the same check PirateFace's official client does
(`pirateface/huggingface.py`): SHA-256 for Git LFS files, the git blob SHA-1
(`sha1("blob <size>\0" + content)`) for every other file. `model.Verify` fails
closed on any discrepancy, and `supervisor.Runner` will not start inference or
seeding if verification fails.

## Where the record comes from

`model.PirateFaceResolver` makes two calls, each matching an official client:

| Data | Request | Source of the format |
| --- | --- | --- |
| Magnet | `GET pirateface.co/api/torrents?repo=<id>` → `{"magnet": ...}`, 404 = not listed | `pirateface` package, `client/api.py` |
| File fingerprints | `GET huggingface.co/api/models/<id>/tree/<rev>?recursive=true`, paginated by `Link: rel="next"`; entries `type`, `path`, `size`, `oid`, `lfs.oid` | `huggingface_hub`, `list_repo_tree` / `RepoFile` |

No other PirateFace field is used, because none is documented. Set
`model.revision` to the commit shown on the model's PirateFace page: the torrent
was built from that revision. With the default (`main`), an upstream change
makes verification fail closed rather than pass the wrong bytes.

Facts from PirateFace's client that shape the seeder:

- A magnet alone carries no file list; the client needs a live peer to send
  it, then caches the `.torrent`. A lone seeder holding only a magnet cannot
  start. See the end-to-end test plan.
- Torrents are keyed by v1 infohash and may contain pad files (hybrid
  v1/v2). seedtensor does not assume v2.
- Files sit under the torrent's root folder: `<save_path>/<torrent name>/<repo
  path>`. The weights directory's name must equal the torrent name for
  in-place seeding.
- It is unconfirmed whether a torrent contains every file in the Hugging Face
  tree (e.g. `README.md`, `.gitattributes`). Verification currently requires
  every tree file; the end-to-end test checks this.

## Process boundaries

Three processes, by design:

1. **seedtensor (Go):** orchestration only. Resolves, verifies, supervises.
2. **TensorFold:** inference. On Linux, launched inside a new network namespace
   (`unshare -r -n`, loopback brought up) so it can serve `127.0.0.1` but reach
   nothing else. This is what backs the "nothing you send leaves the machine"
   claim. `internal/platform` detects whether this is enforceable and the runner
   refuses to start if `no_network` is requested but cannot be enforced.
3. **seedtensor-lt-helper (Python/libtorrent):** the only process with outward
   network access, and it speaks only BitTorrent. It gets the verified weights
   directory read-only and seeds in place (never re-downloads). seedtensor drives
   it over a unix socket with a small line-delimited JSON protocol
   (`internal/seed/socket_helper.go`).

Keeping the seeder in its own process is deliberate: a public-facing network
service should not share an address space with the orchestrator, and libtorrent
is C++ anyway.

## Inference ↔ seeder coupling

`infer.Activity` tracks whether the engine is generating. `seed.Controller`
reads it each tick through `Sensors.IdleFunc` and pauses the helper during
generation (plus on battery, on metered links, and at the daily cap). A grace
period after the last generation avoids flapping between back-to-back requests.

## Why libtorrent runs as a helper, not cgo

libtorrent is a large C++ library. Binding it into the Go binary via cgo would
make the build heavy and couple the orchestrator's memory space to a
network-facing parser. A separate process with a tiny control protocol keeps the
Go side dependency-free (the whole module has **zero** third-party Go deps) and
isolates the attack surface.

## Model compatibility and how to widen it

seedtensor can serve a model only if TensorFold can load it, and seed it only if
it's on PirateFace. Today PirateFace skews toward large safetensors releases,
while TensorFold loads its own `oQ*`/MLX quant formats. To widen the usable set,
produce TensorFold-format quants of PirateFace-listed base models and submit
those quants back to PirateFace (it accepts community submissions with a pinned
revision, checksums, and license evidence). That submission path is a natural
Phase 1.5 task and needs no change to this codebase.

## What is intentionally deferred

- A torrent-backed `Fetcher` (download-then-seed in one step). The interface
  exists (`supervisor.Fetcher`); Phase 1 verifies whatever is on disk and leaves
  fetching to the helper or a manual copy.
- macOS per-process network isolation (needs more than an unprivileged API
  provides; documented as a gap rather than faked).
- Metered-connection detection (no portable source; defaults to "not metered").
- The Phase 2 security harness on top of the local endpoint.
