# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  writeShellScriptBin,
  qemu,
  image,
  imageFile ? "image.raw",
  OVMF,
}:

writeShellScriptBin "repart-image-qemu" ''
  set -euo pipefail

  IMAGE="${image}/${imageFile}"

  if [[ ! -f "$IMAGE" ]]; then
    echo "Image not found: $IMAGE" >&2
    exit 1
  fi

  ${qemu}/bin/qemu-system-x86_64 \
    -smp 4 \
    -m 2048 \
    --enable-kvm \
    -cpu host \
    -bios "${OVMF.fd}/FV/OVMF.fd" \
    -hda "$IMAGE" \
    -snapshot \
    -serial mon:stdio \
    -display gtk
''
