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
  symlinkJoin,
  systemd,
  wrapGAppsHook4,
}:
let
  desktopItem = makeDesktopItem {
    name = "fleeti-updater";
    desktopName = "Fleeti Updater";
    comment = "Install Fleeti system updates";
    exec = "fleeti-updater";
    icon = "system-software-update";
    terminal = false;
    categories = [
      "System"
      "Utility"
    ];
  };

  package = python3Packages.buildPythonApplication {
    pname = "fleeti-updater";
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
      )
    '';

    installPhase = ''
      runHook preInstall
      install -Dm755 fleeti-updater.py $out/bin/fleeti-updater
      patchShebangs $out/bin/fleeti-updater
      runHook postInstall
    '';

    meta = {
      description = "GTK updater for Fleeti systemd-sysupdate releases";
      mainProgram = "fleeti-updater";
      platforms = lib.platforms.linux;
    };
  };
in
symlinkJoin {
  name = "fleeti-updater";
  paths = [
    package
    desktopItem
  ];

  meta = package.meta;
}
