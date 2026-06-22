#!/usr/bin/env python3
# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
#
# fleeti-update: delta (binary-patch) updater for Fleeti devices.
#
# Instead of downloading the whole ~2 GB nix-store image for every update, this
# tool reconstructs the target image locally from content-defined chunks: the
# bulk is copied from the chunks already present on the active nix-store
# partition (the seed), and only the changed chunks are pulled from the server's
# shared chunk store. The reconstructed image is then handed to systemd-sysupdate
# (via a local-source transfer definition) which performs the actual A/B apply:
# slot selection, partition relabel, UKI install and rollback bookkeeping.
#
# Flow:
#   1. Resolve the current and target Fleeti versions.
#   2. Download the small target chunk indexes (nix-store + UKI).
#   3. Reconstruct the nix-store image into the inactive partition, seeded from
#      the active partition, then zstd-compress it into the staging directory.
#   4. Reconstruct the (signed) UKI, seeded from the installed UKI, and compress
#      it into the staging directory.
#   5. Write a local SHA256SUMS and run systemd-sysupdate against the staging
#      directory so it applies the update from local files (no network).
#
# Any failure exits non-zero; the caller (fleeti-admind) then falls back to the
# full-image download path. Uses the Python standard library only and shells out
# to desync, zstd and systemd-sysupdate.

import glob
import hashlib
import json
import os
import subprocess
import sys
import urllib.error
import urllib.request


def env(name, default=""):
    value = os.environ.get(name)
    if value is None or value == "":
        return default
    return value


def log(message):
    sys.stderr.write("fleeti-update: %s\n" % message)
    sys.stderr.flush()


# Progress is reported on stdout as newline-delimited JSON, each line prefixed with
# PROGRESS_PREFIX, so the parent (fleeti-admind) can stream and surface it while the
# update runs. Human-readable logs stay on stderr (see log()). The overall fraction is
# phase-weighted (see run_update / reconstruct_*); it advances at milestone boundaries
# rather than tracking byte-level progress of desync/zstd.
PROGRESS_PREFIX = "@@PROGRESS@@ "


def emit_progress(state, phase, fraction):
    fraction = max(0.0, min(1.0, float(fraction)))
    line = PROGRESS_PREFIX + json.dumps({"state": state, "phase": phase, "fraction": fraction})
    sys.stdout.write(line + "\n")
    sys.stdout.flush()


class UpdateError(Exception):
    pass


def read_os_release_field(path, name):
    prefix = name + "="
    try:
        with open(path, encoding="utf-8") as handle:
            for line in handle:
                if not line.startswith(prefix):
                    continue
                value = line[len(prefix):].strip()
                # os-release values may be quoted.
                if len(value) >= 2 and value[0] in "\"'" and value[-1] == value[0]:
                    value = value[1:-1]
                return value or None
    except OSError:
        return None
    return None


def run(args, timeout=3600):
    log("exec: %s" % " ".join(args))
    try:
        proc = subprocess.run(args, capture_output=True, text=True, timeout=timeout, check=False)
    except (OSError, subprocess.SubprocessError) as exc:
        raise UpdateError("failed to run %s: %s" % (args[0], exc))
    if proc.returncode != 0:
        detail = (proc.stderr.strip() or proc.stdout.strip() or "no output")
        raise UpdateError("%s failed (rc=%d): %s" % (args[0], proc.returncode, detail))
    return proc.stdout


class Updater:
    def __init__(self):
        self.base_url = env("FLEETI_UPDATE_BASE_URL").rstrip("/")
        # desync's HTTP chunk store expects a directory URL with a trailing slash
        # (it appends <aa>/<chunk-id>.cacnk).
        self.store_url = env("FLEETI_UPDATE_STORE_URL").rstrip("/") + "/"
        self.image_id = env("FLEETI_UPDATE_IMAGE_ID", "fleeti")
        self.uki_name = env("FLEETI_UPDATE_UKI_NAME", self.image_id)
        self.staging_dir = env("FLEETI_UPDATE_STAGING_DIR", "/var/cache/fleeti-update")
        self.definitions_dir = env("FLEETI_UPDATE_DEFINITIONS_DIR", "/etc/fleeti/sysupdate-local")
        self.os_release = env("FLEETI_UPDATE_OS_RELEASE", "/etc/os-release")
        self.boot_dir = env("FLEETI_UPDATE_BOOT_DIR", "/boot")
        self.partlabel_dir = env("FLEETI_UPDATE_PARTLABEL_DIR", "/dev/disk/by-partlabel")

        self.desync = env("FLEETI_DESYNC", "desync")
        self.zstd = env("FLEETI_ZSTD", "zstd")
        self.sysupdate = env("FLEETI_SYSTEMD_SYSUPDATE")

    # --- discovery ---

    def current_version(self):
        version = read_os_release_field(self.os_release, "IMAGE_VERSION")
        if not version:
            raise UpdateError("could not determine current IMAGE_VERSION")
        return version

    def discover_target_version(self):
        # Ask systemd-sysupdate (default network definitions) what the newest
        # available version is, so the delta path targets the same release the
        # full-download path would.
        if not self.sysupdate:
            raise UpdateError("systemd-sysupdate path is not configured")
        out = run([self.sysupdate, "--json=short", "--no-pager", "check-new"], timeout=120)
        try:
            data = json.loads(out)
        except (json.JSONDecodeError, TypeError):
            return None
        if isinstance(data, dict):
            available = data.get("available")
            if isinstance(available, str) and available.strip():
                return available.strip()
        return None

    def active_partition(self, current):
        path = os.path.join(self.partlabel_dir, "nix-store_%s" % current)
        if not os.path.exists(path):
            raise UpdateError("active nix-store partition not found: %s" % path)
        return path

    def inactive_partition(self, current):
        # The nix-store partitions form an A/B pair (linux-generic). The slot not
        # labelled for the running version is the inactive one; before the first
        # update it is still the spare labelled "_empty".
        active_label = "nix-store_%s" % current
        candidates = []
        for path in glob.glob(os.path.join(self.partlabel_dir, "nix-store_*")):
            if os.path.basename(path) != active_label:
                candidates.append(path)
        empty = os.path.join(self.partlabel_dir, "_empty")
        if os.path.exists(empty):
            candidates.append(empty)
        # De-duplicate while preserving order.
        seen = set()
        unique = []
        for path in candidates:
            real = os.path.realpath(path)
            if real in seen:
                continue
            seen.add(real)
            unique.append(path)
        if len(unique) != 1:
            raise UpdateError("expected exactly one inactive nix-store slot, found %d" % len(unique))
        return unique[0]

    # --- reconstruction ---

    def download(self, url, destination):
        log("download: %s" % url)
        try:
            with urllib.request.urlopen(url, timeout=120) as response, open(destination, "wb") as out:
                while True:
                    chunk = response.read(1024 * 256)
                    if not chunk:
                        break
                    out.write(chunk)
        except (urllib.error.URLError, OSError) as exc:
            raise UpdateError("failed to download %s: %s" % (url, exc))

    def make_seed_index(self, blob_path, index_path):
        # Build an index describing the seed blob's current chunks. desync uses it
        # to copy matching chunks from the seed instead of downloading them.
        run([self.desync, "make", index_path, blob_path])

    def extract(self, target_index, output_path, seed_index, seed_blob):
        args = [
            self.desync, "extract",
            "--store", self.store_url,
            "--seed", "%s:%s" % (seed_index, seed_blob),
            "--skip-invalid-seeds",
            target_index, output_path,
        ]
        run(args)

    def compress(self, source_path, destination_path):
        # Streaming zstd: read the (block device or file) source, write a small
        # compressed artifact systemd-sysupdate decompresses on apply.
        log("compress: %s -> %s" % (source_path, destination_path))
        with open(source_path, "rb") as src, open(destination_path, "wb") as dst:
            try:
                proc = subprocess.run(
                    [self.zstd, "-q", "-T0", "-c"],
                    stdin=src, stdout=dst, stderr=subprocess.PIPE, check=False,
                )
            except (OSError, subprocess.SubprocessError) as exc:
                raise UpdateError("failed to run zstd: %s" % exc)
        if proc.returncode != 0:
            raise UpdateError("zstd failed (rc=%d): %s" % (proc.returncode, proc.stderr.decode("utf-8", "replace").strip()))

    def reconstruct_nix_store(self, current, target):
        target_index = os.path.join(self.staging_dir, "%s_%s.nix-store.raw.caibx" % (self.image_id, target))
        seed_index = os.path.join(self.staging_dir, "seed-nix-store.caibx")
        staged = os.path.join(self.staging_dir, "%s_%s.nix-store.raw.zst" % (self.image_id, target))

        # The nix-store image is the bulk of the work; it spans the 0.02..0.75 band of
        # the overall progress bar.
        emit_progress("downloading", "Reconstructing system image", 0.05)
        self.download("%s/%s_%s.nix-store.raw.caibx" % (self.base_url, self.image_id, target), target_index)

        active = self.active_partition(current)
        inactive = self.inactive_partition(current)
        log("seeding from active partition %s; reconstructing into %s" % (active, inactive))

        self.make_seed_index(active, seed_index)
        emit_progress("downloading", "Downloading changed chunks", 0.12)
        self.extract(target_index, inactive, seed_index, active)
        emit_progress("downloading", "Reconstructing system image", 0.62)
        self.compress(inactive, staged)
        emit_progress("downloading", "Reconstructing system image", 0.75)
        return os.path.basename(staged)

    def reconstruct_uki(self, current, target):
        target_index = os.path.join(self.staging_dir, "%s_%s.efi.caibx" % (self.uki_name, target))
        seed_index = os.path.join(self.staging_dir, "seed-uki.caibx")
        reconstructed = os.path.join(self.staging_dir, "%s_%s.efi" % (self.uki_name, target))
        staged = os.path.join(self.staging_dir, "%s_%s.efi.zst" % (self.uki_name, target))

        # The UKI is small; it spans the 0.75..0.90 band of the overall progress bar.
        emit_progress("downloading", "Reconstructing boot image", 0.77)
        self.download("%s/%s_%s.efi.caibx" % (self.base_url, self.uki_name, target), target_index)

        seed_blob = os.path.join(self.boot_dir, "EFI", "Linux", "%s_%s.efi" % (self.uki_name, current))
        if not os.path.exists(seed_blob):
            raise UpdateError("installed UKI seed not found: %s" % seed_blob)

        self.make_seed_index(seed_blob, seed_index)
        self.extract(target_index, reconstructed, seed_index, seed_blob)
        emit_progress("downloading", "Reconstructing boot image", 0.87)
        self.compress(reconstructed, staged)
        os.remove(reconstructed)
        emit_progress("downloading", "Reconstructing boot image", 0.90)
        return os.path.basename(staged)

    def stage_manifest(self, names):
        # Generate SHA256SUMS with bare filenames (no path) for systemd-sysupdate.
        manifest = os.path.join(self.staging_dir, "SHA256SUMS")
        lines = []
        for name in sorted(names):
            digest = hashlib.sha256()
            with open(os.path.join(self.staging_dir, name), "rb") as handle:
                for block in iter(lambda: handle.read(1024 * 1024), b""):
                    digest.update(block)
            lines.append("%s  %s\n" % (digest.hexdigest(), name))
        with open(manifest, "w", encoding="utf-8") as handle:
            handle.writelines(lines)

    def clean_staging(self):
        try:
            os.makedirs(self.staging_dir, exist_ok=True)
        except OSError as exc:
            raise UpdateError("failed to create staging directory: %s" % exc)
        for path in glob.glob(os.path.join(self.staging_dir, "*")):
            try:
                os.remove(path)
            except OSError:
                pass

    def apply(self, target):
        if not self.sysupdate:
            raise UpdateError("systemd-sysupdate path is not configured")
        run([self.sysupdate, "--definitions=%s" % self.definitions_dir, "--no-pager", "update", target])

    # --- entry point ---

    def run_update(self, requested_target):
        if not self.base_url or not self.store_url:
            raise UpdateError("delta update is not configured (missing base/store URL)")

        current = self.current_version()
        emit_progress("downloading", "Preparing", 0.0)
        target = (requested_target or "").strip() or self.discover_target_version()
        if not target:
            log("no target version available; nothing to do")
            return 0
        if target == current:
            log("already at version %s; nothing to do" % current)
            return 0

        log("delta update %s -> %s" % (current, target))
        self.clean_staging()
        emit_progress("downloading", "Preparing", 0.02)

        nix_store_artifact = self.reconstruct_nix_store(current, target)
        uki_artifact = self.reconstruct_uki(current, target)
        self.stage_manifest([nix_store_artifact, uki_artifact])
        emit_progress("applying", "Applying update", 0.92)

        log("staged artifacts; applying via systemd-sysupdate")
        self.apply(target)
        emit_progress("applying", "Applying update", 1.0)
        log("delta update to %s applied" % target)
        return 0


def main(argv):
    requested_target = argv[1] if len(argv) > 1 else ""
    updater = Updater()
    try:
        return updater.run_update(requested_target)
    except UpdateError as exc:
        log("error: %s" % exc)
        return 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
