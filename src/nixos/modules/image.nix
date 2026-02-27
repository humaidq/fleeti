# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  modulesPath,
  pkgs,
  config,
  lib,
  ...
}:

{
  imports = [
    (modulesPath + "/image/repart.nix")
  ];

  system.image.id = lib.mkDefault "fleeti";

  image.repart =
    let
      inherit (pkgs.stdenv.hostPlatform) efiArch;
      size = "2G";
    in
    {
      name = config.system.image.id;
      split = true;

      partitions = {
        esp = {
          contents = {
            "/EFI/BOOT/BOOT${lib.toUpper efiArch}.EFI".source =
              "${pkgs.systemd}/lib/systemd/boot/efi/systemd-boot${efiArch}.efi";

            "/EFI/Linux/${config.system.boot.loader.ukiFile}".source =
              "${config.system.build.uki}/${config.system.boot.loader.ukiFile}";

            "/loader/loader.conf".source = builtins.toFile "loader.conf" ''
              timeout 20
            '';
          };
          repartConfig = {
            Type = "esp";
            Label = "boot";
            Format = "vfat";
            SizeMinBytes = "200M";
            SplitName = "-";
          };
        };

        nix-store = {
          storePaths = [ config.system.build.toplevel ];
          nixStorePrefix = "/";
          repartConfig = {
            Type = "linux-generic";
            Label = "nix-store_${config.system.image.version}";
            Minimize = "off";
            SizeMinBytes = size;
            SizeMaxBytes = size;
            Format = "squashfs";
            ReadOnly = "yes";
            SplitName = "nix-store";
          };
        };

        empty.repartConfig = {
          Type = "linux-generic";
          Label = "_empty";
          Minimize = "off";
          SizeMinBytes = size;
          SizeMaxBytes = size;
          SplitName = "-";
        };

        root.repartConfig = {
          Type = "root";
          Format = "ext4";
          Label = "root";
          Minimize = "off";
          SizeMinBytes = "5G";
          SizeMaxBytes = "5G";
          SplitName = "-";
        };
      };
    };
}
