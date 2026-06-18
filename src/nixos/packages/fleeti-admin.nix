# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  adwaita-icon-theme,
  desktop-file-utils,
  gobject-introspection,
  gsettings-desktop-schemas,
  gtk4,
  hicolor-icon-theme,
  lib,
  makeDesktopItem,
  python3Packages,
  sudoPath ? "sudo",
  sbEnrollPath ? "fleeti-sb-enroll",
  symlinkJoin,
  systemd,
  wrapGAppsHook4,
}:
let
  desktopItem = makeDesktopItem {
    name = "fleeti-admin";
    desktopName = "Fleeti Admin";
    comment = "Manage Fleeti updates and device provisioning";
    exec = "fleeti-admin";
    icon = "preferences-system";
    terminal = false;
    categories = [
      "System"
      "Settings"
    ];
  };

  package = python3Packages.buildPythonApplication {
    pname = "fleeti-admin";
    version = "1.0.0";

    pyproject = false;
    src = ./.;

    strictDeps = true;

    nativeBuildInputs = [
      desktop-file-utils
      gobject-introspection
      wrapGAppsHook4
    ];

    buildInputs = [
      adwaita-icon-theme
      gsettings-desktop-schemas
      gtk4
      hicolor-icon-theme
    ];

    propagatedBuildInputs = with python3Packages; [
      pycairo
      pygobject3
    ];

    dontWrapGApps = true;

    preFixup = ''
      makeWrapperArgs+=(
        "''${gappsWrapperArgs[@]}"
        "--set" "FLEETI_SUDO" "${sudoPath}"
        "--set" "FLEETI_SYSTEMD_SYSUPDATE" "${systemd}/lib/systemd/systemd-sysupdate"
        "--set" "FLEETI_SYSTEMCTL" "${systemd}/bin/systemctl"
        "--set" "FLEETI_SB_ENROLL" "${sbEnrollPath}"
        "--set" "FLEETI_ADMIND_STATUS" "/run/fleeti/admind/status.json"
      )
    '';

    installPhase = ''
      runHook preInstall
      install -Dm755 fleeti-admin.py $out/bin/fleeti-admin
      patchShebangs $out/bin/fleeti-admin
      runHook postInstall
    '';

    meta = {
      description = "GTK admin app for Fleeti updates and device provisioning";
      mainProgram = "fleeti-admin";
      platforms = lib.platforms.linux;
    };
  };
in
symlinkJoin {
  name = "fleeti-admin";
  paths = [
    package
    desktopItem
  ];

  inherit (package) meta;
}
