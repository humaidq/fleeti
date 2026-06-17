# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  lib,
  python3,
  writeShellApplication,
}:
writeShellApplication {
  name = "fleeti-admind";

  runtimeInputs = [ python3 ];

  text = ''
    exec ${python3}/bin/python3 ${./fleeti-admind.py} "$@"
  '';

  meta = {
    description = "Fleeti device management agent";
    mainProgram = "fleeti-admind";
    platforms = lib.platforms.linux;
  };
}
