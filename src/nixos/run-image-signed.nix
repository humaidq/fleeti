# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
#
# Local test harness: boot a *signed* Fleeti image in an OVMF VM so the Secure
# Boot enrollment flow (fleeti-admin -> Provision -> Enroll Secure Boot keys) can
# be exercised end to end. Unlike production, signing here uses a throwaway key
# generated at run time (never written to the Nix store), mirroring the
# out-of-Nix signing the build pipeline does.
{
  coreutils,
  efitools,
  jq,
  mtools,
  openssl,
  qemu,
  sbsigntool,
  util-linux,
  writeShellApplication,
  xz,
  image,
  imageFile ? "image.raw",
  OVMF,
}:

writeShellApplication {
  name = "repart-image-qemu-signed";

  runtimeInputs = [
    coreutils
    efitools
    jq
    mtools
    openssl
    qemu
    sbsigntool
    util-linux
    xz
  ];

  text = ''
    src_image="${image}/${imageFile}"
    if [[ ! -f "$src_image" ]]; then
      echo "Image not found: $src_image" >&2
      exit 1
    fi

    work="$(mktemp -d)"
    trap 'rm -rf "$work"' EXIT

    disk="$work/disk.raw"
    echo "==> Copying image to a writable scratch disk"
    cp --reflink=auto "$src_image" "$disk"
    chmod u+w "$disk"

    echo "==> Generating an ephemeral Secure Boot key (throwaway, test only)"
    openssl req -x509 -newkey rsa:2048 -nodes -sha256 -days 3650 \
      -keyout "$work/db.key" -out "$work/db.crt" \
      -subj "/CN=Fleeti Dev Secure Boot/O=Fleeti"
    uuidgen > "$work/guid"

    echo "==> Signing image (EFI binaries + auto-enrollment keys)"
    bash ${./sign-secure-boot.sh} image "$disk" "$work/db.crt" "$work/db.key" "$work/guid"

    # Disable systemd-boot auto-enrollment in the test image so the *manual*
    # fleeti-admin button is the path under test (otherwise "if-safe" silently
    # auto-enrolls inside a VM and the button never appears). The keys staged at
    # /loader/keys/auto/ remain for the helper's efi-updatevar enrollment.
    echo "==> Disabling auto-enrollment in the test image's loader.conf"
    esp_type_guid="C12A7328-F81F-11D2-BA4B-00A0C93EC93B"
    json="$(sfdisk -J "$disk")"
    sector="$(printf '%s' "$json" | jq -r '.partitiontable.sectorsize // 512')"
    start="$(printf '%s' "$json" | jq -r --arg g "$esp_type_guid" \
      '[.partitiontable.partitions[] | select((.type | ascii_upcase) == $g or .name == "boot") | .start] | first // empty')"
    if [[ -z "$start" ]]; then
      echo "Could not locate ESP partition in $disk" >&2
      exit 1
    fi
    mi="$disk@@$((start * sector))"
    printf 'timeout 20\nsecure-boot-enroll off\n' > "$work/loader.conf"
    mcopy -i "$mi" -o "$work/loader.conf" "::/loader/loader.conf"

    echo "==> Preparing writable OVMF variables (setup mode)"
    vars="$work/OVMF_VARS.fd"
    cp "${OVMF.fd}/FV/OVMF_VARS.fd" "$vars"
    chmod u+w "$vars"

    echo "==> Booting signed image in QEMU (OVMF, Secure Boot capable)"
    exec qemu-system-x86_64 \
      -smp 4 \
      -m 2048 \
      --enable-kvm \
      -cpu host \
      -machine q35,smm=on \
      -global driver=cfi.pflash01,property=secure,value=on \
      -drive if=pflash,format=raw,unit=0,readonly=on,file="${OVMF.fd}/FV/OVMF_CODE.fd" \
      -drive if=pflash,format=raw,unit=1,file="$vars" \
      -drive file="$disk",format=raw,if=virtio \
      -serial mon:stdio \
      -display gtk
  '';
}
