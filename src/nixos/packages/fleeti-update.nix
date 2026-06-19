# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  lib,
  python3,
  desync,
  zstd,
  writeShellApplication,
}:
writeShellApplication {
  name = "fleeti-update";

  runtimeInputs = [
    python3
    desync
    zstd
  ];

  text = ''
    exec ${python3}/bin/python3 ${./fleeti-update.py} "$@"
  '';

  meta = {
    description = "Fleeti delta (binary-patch) updater";
    mainProgram = "fleeti-update";
    platforms = lib.platforms.linux;
  };
}
