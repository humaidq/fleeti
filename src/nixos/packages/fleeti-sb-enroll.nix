# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
#
# Privileged helper that enrols the profile's staged Secure Boot keys into the
# firmware. It is run as root via sudo from the Fleeti Admin GUI. Enrolment only
# succeeds while the firmware is in setup mode; the keys themselves are the
# PK/KEK/db .auth payloads injected onto the ESP at build time.
{
  coreutils,
  efitools,
  writeShellApplication,
}:

writeShellApplication {
  name = "fleeti-sb-enroll";

  runtimeInputs = [
    coreutils
    efitools
  ];

  text = ''
    keys_dir="''${FLEETI_SB_KEYS_DIR:-/boot/loader/keys/auto}"
    efivars="/sys/firmware/efi/efivars"
    efi_guid="8be4df61-93ca-11d2-aa0d-00e098032b8c"

    # Read the single value byte of an EFI variable (after the 4 attribute bytes).
    read_efivar_bool() {
      local path="$efivars/$1-$efi_guid"
      [ -r "$path" ] || return 1
      dd if="$path" bs=1 skip=4 count=1 2>/dev/null | od -An -tu1 | tr -d ' '
    }

    if [ ! -d "$efivars" ]; then
      echo "fleeti-sb-enroll: this system did not boot via UEFI; cannot enrol Secure Boot keys" >&2
      exit 1
    fi

    setup_mode="$(read_efivar_bool SetupMode || true)"
    if [ "$setup_mode" != "1" ]; then
      echo "fleeti-sb-enroll: firmware is not in setup mode; enrol from UEFI setup first" >&2
      exit 1
    fi

    for var in db KEK PK; do
      if [ ! -f "$keys_dir/$var.auth" ]; then
        echo "fleeti-sb-enroll: missing key payload $keys_dir/$var.auth" >&2
        exit 1
      fi
    done

    # Enrol db and KEK first, then PK last: writing PK takes the firmware out of
    # setup mode and enables Secure Boot enforcement.
    for var in db KEK PK; do
      echo "fleeti-sb-enroll: enrolling $var"
      efi-updatevar -f "$keys_dir/$var.auth" "$var"
    done

    echo "fleeti-sb-enroll: Secure Boot keys enrolled; reboot to activate."
  '';
}
