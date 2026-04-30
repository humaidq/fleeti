# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  lib,
  makeBinaryWrapper,
  python3,
  runCommand,
  util-linux,
}:
runCommand "openclaw-mountd"
  {
    nativeBuildInputs = [ makeBinaryWrapper ];
  }
  ''
    mkdir -p "$out/bin"
    makeBinaryWrapper ${python3}/bin/python3 "$out/bin/openclaw-mountd" \
      --add-flags ${./openclaw-mountd.py} \
      --prefix PATH : ${lib.makeBinPath [ util-linux ]}
  ''
// {
  meta = {
    description = "OpenClaw guest mount helper";
    platforms = lib.platforms.linux;
  };
}
