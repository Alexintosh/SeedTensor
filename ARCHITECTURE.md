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
trusts them only when every file matches a SHA-256 from the model's published
record (`model.Resolved`), and that record is pinned to a named origin (a
PirateFace listing or a local fixture). `model.Verify` fails closed on any
discrepancy, and `supervisor.Runner` will not start inference or seeding if
verification fails.

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
