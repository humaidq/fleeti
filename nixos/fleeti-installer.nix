# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  coreutils,
  hwinfo,
  ncurses,
  util-linux,
  writeShellApplication,
  zstd,
}:
writeShellApplication {
  name = "fleeti-installer";

  runtimeInputs = [
    coreutils
    hwinfo
    ncurses
    util-linux
    zstd
  ];

  text = builtins.readFile ./fleeti-installer.sh;

  meta = {
    description = "Interactive installer that flashes the bundled Fleeti image";
    platforms = [
      "x86_64-linux"
      "aarch64-linux"
    ];
  };
}
