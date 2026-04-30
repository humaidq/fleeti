# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  glib,
  lib,
  python3,
  writeShellApplication,
}:
let
  pythonEnv = python3.withPackages (
    ps: with ps; [
      pygobject3
    ]
  );

  giTypelibPath = lib.concatStringsSep ":" [
    "${glib}/lib/girepository-1.0"
  ];
in
writeShellApplication {
  name = "molthousectl";

  runtimeInputs = [ pythonEnv ];

  text = ''
    export GI_TYPELIB_PATH="${giTypelibPath}''${GI_TYPELIB_PATH:+:''${GI_TYPELIB_PATH}}"

    exec ${pythonEnv}/bin/python3 ${./molthousectl.py} "$@"
  '';

  meta = {
    description = "MoltHouse local control CLI";
    platforms = lib.platforms.linux;
  };
}
