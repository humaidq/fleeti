# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  glib,
  lib,
  python3,
  symlinkJoin,
  writeTextDir,
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

  launcher = writeShellApplication {
    name = "molthoused";

    runtimeInputs = [ pythonEnv ];

    text = ''
      export GI_TYPELIB_PATH="${giTypelibPath}''${GI_TYPELIB_PATH:+:''${GI_TYPELIB_PATH}}"

      exec ${pythonEnv}/bin/python3 ${./molthoused.py} "$@"
    '';

    meta = {
      description = "MoltHouse privileged helper service";
      mainProgram = "molthoused";
      platforms = lib.platforms.linux;
    };
  };

  dbusService = writeTextDir "share/dbus-1/system-services/ae.fleeti.MoltHouse1.service" ''
    [D-BUS Service]
    Name=ae.fleeti.MoltHouse1
    SystemdService=molthoused.service
  '';

  dbusPolicy = writeTextDir "share/dbus-1/system.d/ae.fleeti.MoltHouse1.conf" ''
    <!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-BUS Bus Configuration 1.0//EN"
     "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
    <busconfig>
      <policy user="root">
        <allow own="ae.fleeti.MoltHouse1"/>
        <allow send_destination="ae.fleeti.MoltHouse1"/>
        <allow receive_sender="ae.fleeti.MoltHouse1"/>
      </policy>

      <policy context="default">
        <allow send_destination="ae.fleeti.MoltHouse1"/>
        <allow receive_sender="ae.fleeti.MoltHouse1"/>
      </policy>
    </busconfig>
  '';
in
symlinkJoin {
  name = "molthoused";
  paths = [
    launcher
    dbusService
    dbusPolicy
  ];

  meta = launcher.meta;
}
