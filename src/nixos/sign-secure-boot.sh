#!/usr/bin/env bash
#
# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
#
# Post-build Secure Boot signing for Fleeti images.
#
# This runs OUTSIDE the Nix build on purpose: the private key must never be read
# by Nix, otherwise it would be copied into the Nix store/cache. The Fleeti web
# service invokes this script after `nix build` with the per-profile key.
#
# Usage:
#   sign-secure-boot.sh image          <raw-image>   <cert.pem> <key.pem> <guid-file>
#   sign-secure-boot.sh update-package <package-dir>  <cert.pem> <key.pem> <guid-file>
#
# image          - sign the EFI binaries inside the raw disk image's ESP
#                  (systemd-boot + UKI) and inject PK/KEK/db auto-enrollment keys.
# update-package - sign the UKI(s) inside a sysupdate package directory and
#                  refresh its SHA256SUMS manifest.

set -euo pipefail

# GPT type GUID of an EFI System Partition.
ESP_TYPE_GUID="C12A7328-F81F-11D2-BA4B-00A0C93EC93B"

die() {
  echo "sign-secure-boot: error: $*" >&2
  exit 1
}

[ "$#" -eq 5 ] || die "expected 5 arguments, got $#"

MODE="$1"
TARGET="$2"
CERT="$3"
KEY="$4"
GUID_FILE="$5"

[ -f "$CERT" ] || die "certificate not found: $CERT"
[ -f "$KEY" ] || die "private key not found: $KEY"
[ -f "$GUID_FILE" ] || die "owner GUID file not found: $GUID_FILE"

IFS= read -r GUID < "$GUID_FILE" || true
GUID="${GUID//[[:space:]]/}"
[ -n "$GUID" ] || die "owner GUID is empty"

# sign_pe <file>: sign an EFI PE binary in place with the profile key.
sign_pe() {
  local target="$1" tmp
  tmp="$(mktemp)"
  sbsign --key "$KEY" --cert "$CERT" --output "$tmp" "$target"
  mv -f "$tmp" "$target"
  sbverify --cert "$CERT" "$target" >/dev/null
}

# make_auth <out-dir>: generate PK/KEK/db enrollment payloads from the single
# profile key. With one key acting as PK=KEK=db, every variable update is signed
# by that same key (PK signs itself, PK signs KEK, KEK signs db).
make_auth() {
  local out="$1"
  cert-to-efi-sig-list -g "$GUID" "$CERT" "$out/sb.esl"
  sign-efi-sig-list -g "$GUID" -k "$KEY" -c "$CERT" PK  "$out/sb.esl" "$out/PK.auth"
  sign-efi-sig-list -g "$GUID" -k "$KEY" -c "$CERT" KEK "$out/sb.esl" "$out/KEK.auth"
  sign-efi-sig-list -g "$GUID" -k "$KEY" -c "$CERT" db  "$out/sb.esl" "$out/db.auth"
}

# esp_offset <raw>: print the ESP partition byte offset within a GPT disk image.
esp_offset() {
  local raw="$1" json sector start
  json="$(sfdisk -J "$raw")"
  sector="$(printf '%s' "$json" | jq -r '.partitiontable.sectorsize // 512')"
  start="$(printf '%s' "$json" | jq -r --arg g "$ESP_TYPE_GUID" \
    '[.partitiontable.partitions[] | select((.type | ascii_upcase) == $g or .name == "boot") | .start] | first // empty')"
  [ -n "$start" ] || die "could not locate ESP partition in $raw"
  printf '%s' "$((start * sector))"
}

# sign_efi_dir <mtools-image> <dir>: sign every *.efi binary in an ESP directory.
sign_efi_dir() {
  local mi="$1" dir="$2" line name tmp
  while IFS= read -r line; do
    name="${line##*/}"
    [ -n "$name" ] || continue
    [[ "${name,,}" == *.efi ]] || continue

    tmp="$(mktemp)"
    mcopy -i "$mi" "$dir/$name" "$tmp"
    sign_pe "$tmp"
    mcopy -i "$mi" -o "$tmp" "$dir/$name"
    rm -f "$tmp"
    echo "sign-secure-boot: signed $dir/$name"
  done < <(mdir -i "$mi" -b "$dir" 2>/dev/null || true)
}

sign_image() {
  local raw="$1"
  [ -f "$raw" ] || die "image not found: $raw"

  local off mi
  off="$(esp_offset "$raw")"
  mi="$raw@@$off"
  echo "sign-secure-boot: ESP found at byte offset $off"

  # systemd-boot bootloader and the unified kernel image(s).
  sign_efi_dir "$mi" "::/EFI/BOOT"
  sign_efi_dir "$mi" "::/EFI/Linux"

  # Auto-enrollment material consumed by systemd-boot's secure-boot-enroll.
  local authdir
  authdir="$(mktemp -d)"
  make_auth "$authdir"
  mmd -i "$mi" "::/loader/keys" 2>/dev/null || true
  mmd -i "$mi" "::/loader/keys/auto" 2>/dev/null || true
  mcopy -i "$mi" -o "$authdir/PK.auth" "$authdir/KEK.auth" "$authdir/db.auth" "::/loader/keys/auto/"
  rm -rf "$authdir"
  echo "sign-secure-boot: injected auto-enrollment keys into /loader/keys/auto"
}

sign_update_package() {
  local dir="$1"
  [ -d "$dir" ] || die "package directory not found: $dir"

  shopt -s nullglob
  local efixz base tmp
  for efixz in "$dir"/*.efi.xz; do
    base="${efixz##*/}"
    base="${base%.xz}"
    tmp="$(mktemp -d)"
    xz -d -c "$efixz" > "$tmp/$base"
    sign_pe "$tmp/$base"
    xz -z -T0 -c "$tmp/$base" > "$tmp/$base.xz"
    chmod 0644 "$tmp/$base.xz"
    mv -f "$tmp/$base.xz" "$efixz"
    rm -rf "$tmp"
    echo "sign-secure-boot: signed ${efixz##*/}"
  done

  # Refresh the checksum manifest so it matches the re-signed artifacts. Bash
  # globs are sorted, so the manifest order stays deterministic.
  local manifest tmpmanifest entry name
  manifest="$dir/SHA256SUMS"
  tmpmanifest="$(mktemp)"
  (
    cd "$dir"
    for entry in *; do
      [ -f "$entry" ] || continue
      [ "$entry" = "SHA256SUMS" ] && continue
      sha256sum "$entry"
    done
  ) > "$tmpmanifest"
  chmod 0644 "$tmpmanifest"
  mv -f "$tmpmanifest" "$manifest"
  echo "sign-secure-boot: refreshed SHA256SUMS"
}

case "$MODE" in
  image)
    sign_image "$TARGET"
    ;;
  update-package)
    sign_update_package "$TARGET"
    ;;
  *)
    die "unknown mode: $MODE (expected 'image' or 'update-package')"
    ;;
esac

echo "sign-secure-boot: done"
