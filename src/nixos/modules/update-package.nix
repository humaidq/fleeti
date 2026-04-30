# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  config,
  pkgs,
  ...
}:

let
  inherit (config.system) build;
  inherit (config.system.image) version id;
in

{
  config.system.build.sysupdate-package =
    pkgs.runCommand "sysupdate-package-${config.system.image.version}"
      {
        nativeBuildInputs = [ pkgs.xz ];
      }
      ''
        mkdir $out
        xz --threads=0 --stdout ${build.uki}/${config.system.boot.loader.ukiFile} > $out/${config.system.boot.loader.ukiFile}.xz
        xz --threads=0 --stdout ${build.image}/${id}_${version}.nix-store.raw > $out/${id}_${version}.nix-store.raw.xz
        cd $out
        sha256sum * > SHA256SUMS
      '';
}
