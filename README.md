# seedtensor

Local AI inference that seeds its own model. A machine running a model locally
already holds the weights on disk; seedtensor puts that otherwise-idle copy to work
keeping the model alive for everyone else, over BitTorrent, while the inference
engine is idle.

**Phase 1 (this repo): inference engine + automatic idle seeding.** One model,
served locally by [TensorFold](https://github.com/ashhart/TensorFold) over an
OpenAI-compatible API, with its weights verified against a checksum record and
seeded via a BitTorrent v2 helper whenever the engine isn't generating.

**Phase 2 (not yet): embedding a security-research harness** on top of the local
endpoint. Deliberately out of scope here.

## What it guarantees

- **Nothing you send to the model leaves the machine.** On Linux the inference
  process runs in a network namespace with only loopback, so it physically
  cannot reach the network. On macOS this cannot be enforced per-process;
  seedtensor says so plainly and, if you set `no_network: true`, refuses to start
  rather than pretend. (See [ARCHITECTURE.md](ARCHITECTURE.md).)
- **You only ever serve or seed verified bytes.** Every file is hashed against
  the model's published SHA-256 record before the engine loads it. Any missing
  file, extra file, size mismatch, or hash mismatch fails the whole model
  closed — no load, no seed.
- **Seeding never competes with your work.** The seeder pauses during
  generation, on battery, on a metered link, and once a daily upload cap is
  hit.

## Install

Requires Go 1.24+ to build the orchestrator, and Python 3.9+ with the
`libtorrent` module to run the seeding helper.

```sh
# Build the orchestrator
go build -o seedtensor ./cmd/seedtensor

# The seeding helper is a separate process (libtorrent is C++/Python, not Go).
# Install its one dependency:
#   Debian/Ubuntu:  sudo apt install python3-libtorrent
#   macOS (brew):   brew install libtorrent-rasterbar && pip3 install libtorrent
#   or:             pip3 install libtorrent
```

TensorFold itself is a separate binary; install it per its own instructions and
make sure `tensorfold` is on your `PATH` (or set `infer.binary` in the config).

## Use

```sh
# 1. Write a starter config
./seedtensor init -model TensorFold/Qwen3.8-27B-oQ4
#    then set "model.revision" in seedtensor.json to the Hugging Face commit
#    shown on the model's PirateFace page (pirateface.co/<owner>/<model>)

# 2. Fetch the weights. In Phase 1 this is done with the torrent helper or by
#    placing an existing copy at paths.weights_dir. Then verify them:
./seedtensor verify

# 3. Start the seeding helper (in its own terminal or a service):
python3 helper/seedtensor_lt_helper.py --socket ~/.cache/seedtensor/state/helper.sock

# 4. Run: resolve -> verify -> serve inference -> seed while idle
./seedtensor run
```

The inference API is then at `http://127.0.0.1:8080/v1` (OpenAI-compatible).

## Platform notes

- **Apple Silicon (Metal):** set `platform: metal` or leave `auto`. Per-process
  no-network isolation is not available; run under an outer sandbox if you need
  that guarantee enforced, or accept the documented gap.
- **NVIDIA (CUDA) on Linux:** set `platform: cuda` or leave `auto`. No-network
  isolation is enforced via a network namespace.

## Model compatibility

seedtensor serves whatever TensorFold can load (its packaged `oQ*`/MLX quants of
Qwen, GLM, Ling, and so on) and seeds whatever is listed on
[PirateFace](https://pirateface.co). The usable set is the intersection; see
[ARCHITECTURE.md](ARCHITECTURE.md) for how to widen it by submitting
TensorFold-format quants to PirateFace.

## Status

All packages build and pass `go test ./...`. The torrent helper and TensorFold
are exercised in tests through fakes; the real libtorrent helper and TensorFold
binary run on your machine (both need a GPU/Apple Silicon and open network that a
CI sandbox lacks).

## Layout

```
cmd/seedtensor/            CLI: init, verify, run
internal/config/        JSON config, defaults, validation (zero deps)
internal/model/         resolve (PirateFace/fixture), fail-closed verify
internal/infer/         TensorFold supervisor, idle/activity tracking
internal/seed/          seeding gate, daily ledger, controller, socket client
internal/platform/      backend + network-isolation detection
internal/supervisor/    the Phase 1 runner that wires it all together
helper/                 libtorrent seeding helper (Python reference impl)
```
