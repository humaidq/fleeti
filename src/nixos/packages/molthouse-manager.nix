# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  adwaita-icon-theme,
  callPackage,
  desktop-file-utils,
  foot,
  gobject-introspection,
  gsettings-desktop-schemas,
  gtk4,
  hicolor-icon-theme,
  lib,
  makeDesktopItem,
  python3Packages,
  symlinkJoin,
  wrapGAppsHook4,
}:
let
  molthousectlPackage = callPackage ./molthousectl.nix { };

  desktopItem = makeDesktopItem {
    name = "molthouse-manager";
    desktopName = "MoltHouse Manager";
    comment = "Manage the local MoltHouse OpenClaw runtime";
    exec = "molthouse-manager";
    terminal = false;
    categories = [
      "System"
      "Utility"
    ];
  };

  package = python3Packages.buildPythonApplication {
    pname = "molthouse-manager";
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
      makeWrapperArgs+=("''${gappsWrapperArgs[@]}")
      makeWrapperArgs+=(--prefix PATH : ${lib.makeBinPath [ foot molthousectlPackage ]})
    '';

    installPhase = ''
      runHook preInstall
      install -Dm755 molthouse-manager.py $out/bin/molthouse-manager
      patchShebangs $out/bin/molthouse-manager
      runHook postInstall
    '';

    meta = {
      description = "MoltHouse GTK manager";
      mainProgram = "molthouse-manager";
      platforms = lib.platforms.linux;
    };
  };
in
symlinkJoin {
  name = "molthouse-manager";
  paths = [
    package
    desktopItem
  ];

  meta = package.meta;
}
