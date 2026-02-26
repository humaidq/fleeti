#!/usr/bin/env bash

set -euo pipefail

if [ "${EUID:-$(id -u)}" -ne 0 ]; then
  echo "Please run as root (for example: sudo fleeti-installer)"
  exit 1
fi

if [ -z "${IMG_PATH:-}" ]; then
  echo "IMG_PATH is not set"
  exit 1
fi

usage() {
  echo ""
  echo "Usage: $(basename "$0") [-w]"
  echo "  -w  Wipe target disk only"
  exit 1
}

WIPE_ONLY=false

while getopts "wh" opt; do
  case "$opt" in
  w)
    WIPE_ONLY=true
    ;;
  h)
    usage
    ;;
  *)
    usage
    ;;
  esac
done

clear || true

cat <<"EOF"
  ______ _      _      _   _
 |  ____| |    | |    | | (_)
 | |__  | | ___| | ___| |_ _
 |  __| | |/ _ \ |/ _ \ __| |
 | |    | |  __/ |  __/ |_| |
 |_|    |_|\___|_|\___|\__|_|
EOF

echo "Welcome to Fleeti installer."
echo ""
echo "Select the target disk to wipe and install Fleeti."
echo ""

if ! hwinfo --disk --short; then
  echo "Falling back to lsblk output:"
  lsblk -d -o NAME,SIZE,MODEL
fi

while true; do
  read -r -p "Target disk [e.g. /dev/nvme0n1]: " DEVICE_NAME

  if [[ ! "$DEVICE_NAME" =~ ^/dev/[a-zA-Z0-9._-]+$ ]]; then
    echo "Invalid device path format."
    continue
  fi

  if [ ! -b "$DEVICE_NAME" ]; then
    echo "Not a block device: $DEVICE_NAME"
    continue
  fi

  device_basename=$(basename "$DEVICE_NAME")
  if [ ! -d "/sys/block/$device_basename" ]; then
    echo "Device not found in /sys/block"
    continue
  fi

  if [ "$(cat "/sys/block/$device_basename/removable")" != "0" ]; then
    read -r -p "Device is removable. Continue? [y/N] " response
    case "$response" in
    [yY][eE][sS] | [yY])
      break
      ;;
    *)
      continue
      ;;
    esac
  fi

  break
done

echo ""
echo "Selected target: $DEVICE_NAME"
read -r -p "This will erase all data on the target. Continue? [y/N] " response

case "$response" in
[yY][eE][sS] | [yY]) ;;
*)
  echo "Aborted."
  exit 0
  ;;
esac

echo "Wiping the first and last 10 MiB..."
SECTOR=512
MIB_TO_SECTORS=20480
SECTORS=$(blockdev --getsz "$DEVICE_NAME")

dd if=/dev/zero of="$DEVICE_NAME" bs="$SECTOR" count="$MIB_TO_SECTORS" conv=fsync status=none
dd if=/dev/zero of="$DEVICE_NAME" bs="$SECTOR" count="$MIB_TO_SECTORS" seek="$((SECTORS - MIB_TO_SECTORS))" conv=fsync status=none
echo "Wipe completed."

if [ "$WIPE_ONLY" = true ]; then
  echo "Wipe-only mode selected."
  echo "Remove installation media and reboot."
  exit 0
fi

shopt -s nullglob
raw_files=("$IMG_PATH"/*.raw.zst)

if [ "${#raw_files[@]}" -eq 0 ]; then
  echo "No .raw.zst image found in $IMG_PATH"
  exit 1
fi

if [ "${#raw_files[@]}" -gt 1 ]; then
  echo "Multiple .raw.zst images found in $IMG_PATH."
  echo "Please keep only one image on the installer media."
  printf ' - %s\n' "${raw_files[@]}"
  exit 1
fi

image_path="${raw_files[0]}"

echo "Flashing image: $image_path"
zstdcat "$image_path" | dd of="$DEVICE_NAME" bs=32M status=progress conv=fsync

sync

echo ""
echo "Installation finished successfully."
echo "Remove installation media and reboot."
