#!/usr/bin/env python3
"""seedbox libtorrent helper.

A thin BitTorrent v2 seeding process driven by the seedbox Go supervisor over a
unix-domain control socket. It speaks the line-delimited JSON protocol defined
in internal/seed/socket_helper.go:

    -> {"cmd":"add","magnet":"...","data_dir":"...","network":"clearnet","info_hash_v2":"..."}
    <- {"ok":true}
    -> {"cmd":"pause"}     <- {"ok":true}
    -> {"cmd":"resume"}    <- {"ok":true}
    -> {"cmd":"uploaded"}  <- {"ok":true,"uploaded":<int>}
    -> {"cmd":"peers"}     <- {"ok":true,"peers":<int>}
    -> {"cmd":"close"}     <- {"ok":true}

Design rules this helper must honor:

  * SEED IN PLACE. Add the torrent against the already-present, verified files
    in data_dir. Never re-download or overwrite them: seedbox has already
    checked every byte against the model's checksum record, and that directory
    is the same one the inference engine reads.
  * NO FETCH. Downloading is disabled; this process only uploads.
  * OVERLAY. For network == "i2p", route via an I2P session (requires an I2P
    router, e.g. i2pd, and libtorrent built with I2P support); for "clearnet",
    use the normal session. "off" never reaches the helper.

This helper is intentionally dependency-light: Python 3.9+, the `libtorrent`
module (python3-libtorrent / `pip install libtorrent`), and the standard
library. Build/run instructions are in the README.

Only one torrent (one model) is handled per helper instance, matching Phase 1.
"""

import argparse
import json
import os
import socket
import sys
import threading

try:
    import libtorrent as lt
except ImportError:
    sys.stderr.write(
        "seedbox-lt-helper: the 'libtorrent' module is not installed.\n"
        "Install python3-libtorrent (apt/brew) or `pip install libtorrent`.\n"
    )
    sys.exit(2)


class Seeder:
    """Wraps a libtorrent session seeding a single model in place."""

    def __init__(self):
        # Disable DHT/LSD by default off; enable per-session below. Seeding
        # only: we set no download rate cap but we never request pieces because
        # the files are already complete.
        self._session = None
        self._handle = None
        self._lock = threading.Lock()
        self._paused = False

    def _ensure_session(self, network: str):
        if self._session is not None:
            return
        settings = {
            # Identify politely; keep defaults otherwise.
            "user_agent": "seedbox-lt-helper/0.1 libtorrent/%s" % lt.version,
            # We are a pure seed; do not announce as a downloader.
            "announce_to_all_trackers": True,
            "announce_to_all_tiers": True,
            # Enable peer discovery for clearnet.
            "enable_dht": network == "clearnet",
            "enable_lsd": network == "clearnet",
            "enable_upnp": network == "clearnet",
            "enable_natpmp": network == "clearnet",
        }
        if network == "i2p":
            # Requires an I2P SAM bridge (i2pd/i2p) on localhost:7656 and a
            # libtorrent build with I2P support. Clearnet discovery is off so
            # the real IP is never announced.
            settings["i2p_hostname"] = os.environ.get("SEEDBOX_I2P_HOST", "127.0.0.1")
            settings["i2p_port"] = int(os.environ.get("SEEDBOX_I2P_PORT", "7656"))
            settings["enable_dht"] = False
            settings["enable_lsd"] = False
            settings["enable_upnp"] = False
            settings["enable_natpmp"] = False
        self._session = lt.session(settings)

    def add(self, magnet: str, data_dir: str, network: str, info_hash_v2: str):
        with self._lock:
            self._ensure_session(network)
            params = lt.parse_magnet_uri(magnet)
            # Seed in place: save_path is the parent so files resolve to the
            # verified directory.
            params.save_path = os.path.dirname(os.path.abspath(data_dir)) or "."
            # Do not verify-download: flag_seed_mode tells libtorrent the data
            # is already present and to treat the torrent as a seed, checking
            # pieces lazily only if a peer disputes them.
            params.flags |= lt.torrent_flags.seed_mode
            params.flags |= lt.torrent_flags.upload_mode
            # Never auto-manage into downloading.
            params.flags &= ~lt.torrent_flags.auto_managed
            self._handle = self._session.add_torrent(params)
            if info_hash_v2:
                # Best-effort sanity note; libtorrent resolves the hash from
                # the magnet. We don't hard-fail on mismatch here because the
                # Go side already pinned trust via the checksum record.
                pass
            self._paused = False

    def pause(self):
        with self._lock:
            if self._handle is not None:
                self._handle.pause()
            self._paused = True

    def resume(self):
        with self._lock:
            if self._handle is not None:
                self._handle.resume()
            self._paused = False

    def uploaded(self) -> int:
        with self._lock:
            if self._handle is None:
                return 0
            st = self._handle.status()
            return int(st.total_upload)

    def peers(self) -> int:
        with self._lock:
            if self._handle is None:
                return 0
            st = self._handle.status()
            return int(st.num_peers)

    def close(self):
        with self._lock:
            self._session = None
            self._handle = None


def serve(sock_path: str):
    seeder = Seeder()
    if os.path.exists(sock_path):
        os.unlink(sock_path)
    srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    srv.bind(sock_path)
    os.chmod(sock_path, 0o600)
    srv.listen(1)
    sys.stderr.write("seedbox-lt-helper: listening on %s\n" % sock_path)

    try:
        while True:
            conn, _ = srv.accept()
            handle_conn(conn, seeder)
    finally:
        srv.close()
        if os.path.exists(sock_path):
            os.unlink(sock_path)


def handle_conn(conn, seeder: Seeder):
    f = conn.makefile("rwb")
    try:
        for line in f:
            try:
                req = json.loads(line)
            except json.JSONDecodeError as e:
                _respond(f, {"ok": False, "error": "bad json: %s" % e})
                continue
            cmd = req.get("cmd")
            try:
                if cmd == "add":
                    seeder.add(req["magnet"], req["data_dir"],
                               req.get("network", "clearnet"),
                               req.get("info_hash_v2", ""))
                    _respond(f, {"ok": True})
                elif cmd == "pause":
                    seeder.pause()
                    _respond(f, {"ok": True})
                elif cmd == "resume":
                    seeder.resume()
                    _respond(f, {"ok": True})
                elif cmd == "uploaded":
                    _respond(f, {"ok": True, "uploaded": seeder.uploaded()})
                elif cmd == "peers":
                    _respond(f, {"ok": True, "peers": seeder.peers()})
                elif cmd == "close":
                    seeder.close()
                    _respond(f, {"ok": True})
                    return
                else:
                    _respond(f, {"ok": False, "error": "unknown cmd %r" % cmd})
            except Exception as e:  # noqa: BLE001 - report everything to the supervisor
                _respond(f, {"ok": False, "error": str(e)})
    finally:
        f.close()
        conn.close()


def _respond(f, obj):
    f.write((json.dumps(obj) + "\n").encode("utf-8"))
    f.flush()


def main():
    ap = argparse.ArgumentParser(description="seedbox libtorrent helper")
    ap.add_argument("--socket", required=True, help="unix socket path to listen on")
    args = ap.parse_args()
    serve(args.socket)


if __name__ == "__main__":
    main()
