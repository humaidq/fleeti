# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  inputs,
  system ? "x86_64-linux",
}:
{
  name ? "fleeti",
  image,
  imageFile,
  extraModules ? [ ],
}:
let
  inherit (inputs.nixpkgs) lib;
  sanitizedImageFile = builtins.replaceStrings [ "/" "." "_" ] [ "-" "-" "-" ] imageFile;

  hostConfiguration = lib.nixosSystem {
    inherit system;

    modules = [
      (
        { modulesPath, pkgs, ... }:
        let
          compressedImage = pkgs.runCommand "${name}-installer-image-${sanitizedImageFile}" { } ''
            mkdir -p "$out"
            ${pkgs.zstd}/bin/zstd --quiet --force --stdout "${image}/${imageFile}" > "$out/${imageFile}.zst"
          '';
        in
        {
          imports = [
            "${toString modulesPath}/installer/cd-dvd/installation-cd-minimal.nix"
          ];

          isoImage.storeContents = [ ];

          isoImage.contents = [
            {
              source = "${compressedImage}/${imageFile}.zst";
              target = "/fleeti-image/${imageFile}.zst";
            }
          ];

          environment.sessionVariables = {
            IMG_PATH = "/iso/fleeti-image";
          };

          systemd.services.wpa_supplicant.wantedBy = lib.mkForce [ "multi-user.target" ];
          systemd.services.sshd.wantedBy = lib.mkForce [ "multi-user.target" ];
          networking.networkmanager.enable = true;
          networking.wireless.enable = lib.mkForce false;

          networking.hostName = "${name}-installer";

          environment.systemPackages = [
            (pkgs.callPackage ./fleeti-installer.nix { })
          ];

          services.getty = {
            greetingLine = "<<< Welcome to the Fleeti installer >>>";
            helpLine = lib.mkAfter ''

              To install Fleeti, run:
              `sudo fleeti-installer`
            '';
          };

          isoImage.squashfsCompression = "zstd -Xcompression-level 6";
          boot.swraid.mdadmConf = "PROGRAM ${pkgs.coreutils}/bin/true";
          boot.supportedFilesystems.zfs = lib.mkForce false;
        }
      )
    ]
    ++ extraModules;
  };
in
{
  inherit hostConfiguration;
  name = "${name}-installer";
  package = hostConfiguration.config.system.build.isoImage;
}
