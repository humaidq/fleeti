# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
_: {
  nixpkgs.overlays = [
    (_final: prev: {
      # systemd 260.x checks for pefile at configure time when ukify is enabled.
      systemdUkify = prev.systemdUkify.overrideAttrs (old: {
        nativeBuildInputs = old.nativeBuildInputs ++ [ prev.buildPackages.python3Packages.pefile ];
      });
    })
  ];
}
