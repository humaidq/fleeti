# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{ config, ... }:

{
  fileSystems =
    let
      configForLabel =
        _: label:
        let
          inherit (config.image.repart.partitions.${label}) repartConfig;
        in
        {
          device = "/dev/disk/by-partlabel/${repartConfig.Label}";
          fsType = repartConfig.Format;
        };
    in
    builtins.mapAttrs configForLabel {
      "/" = "root";
      "/boot" = "esp";
      "/nix/store" = "nix-store";
    };
}
