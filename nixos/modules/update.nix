# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  pkgs,
  config,
  ...
}:

{
  systemd.sysupdate = {
    enable = true;

    transfers =
      let
        commonSource = {
          Path = "https://admin.fleeti.ae/update/";
          Type = "url-file";
        };

        Transfer.Verify = "no";
      in
      {
        "10-nix-store" = {
          Source = commonSource // {
            MatchPattern = [ "${config.system.image.id}_@v.nix-store.raw" ];
          };

          Target = {
            InstancesMax = 2;

            Path = "auto";
            MatchPattern = "nix-store_@v";
            Type = "partition";
            ReadOnly = "yes";
          };

          inherit Transfer;
        };

        "20-boot-image" = {
          Source = commonSource // {
            MatchPattern = [ "${config.boot.uki.name}_@v.efi" ];
          };
          Target = {
            InstancesMax = 2;
            MatchPattern = [ "${config.boot.uki.name}_@v.efi" ];

            Mode = "0444";
            Path = "/EFI/Linux";
            PathRelativeTo = "boot";

            Type = "regular-file";
          };
          inherit Transfer;
        };
      };
  };

  environment.systemPackages = [
    (pkgs.runCommand "systemd-extratools" { } ''
      mkdir -p $out
      ln -s ${config.systemd.package}/lib/systemd $out/bin
    '')
  ];
}
