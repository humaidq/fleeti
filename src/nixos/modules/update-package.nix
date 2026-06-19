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
        nativeBuildInputs = [
          pkgs.xz
          pkgs.desync
        ];
      }
      ''
        mkdir $out
        xz --threads=0 --stdout ${build.uki}/${config.system.boot.loader.ukiFile} > $out/${config.system.boot.loader.ukiFile}.xz
        xz --threads=0 --stdout ${build.image}/${id}_${version}.nix-store.raw > $out/${id}_${version}.nix-store.raw.xz

        # Content-defined chunk index of the (uncompressed) nix-store image, for
        # delta updates. Devices reconstruct the image from chunks they already
        # have on the active partition plus the few changed chunks pulled from the
        # server's shared chunk store. The store (castr/) is content-addressed, so
        # the build server merges it into the global store, deduplicating across
        # versions. The .efi (UKI) index is produced post-build in Fleeti Web,
        # after the UKI is Secure Boot signed (signing rewrites its bytes).
        mkdir -p $out/castr
        desync make --store $out/castr \
          $out/${id}_${version}.nix-store.raw.caibx \
          ${build.image}/${id}_${version}.nix-store.raw

        cd $out
        sha256sum ${config.system.boot.loader.ukiFile}.xz ${id}_${version}.nix-store.raw.xz > SHA256SUMS
      '';
}
