# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  config,
  inputs,
  lib,
  ...
}:
let
  hostName = "fleeti";
  hostSystem = "x86_64-linux";
  hostConfiguration = config.flake.nixosConfigurations.${hostName}.config;
  installerBuilder = import ./mk-fleeti-installer.nix {
    inherit inputs;
    system = hostSystem;
  };
  installerOutput = installerBuilder {
    name = hostName;
    inherit (hostConfiguration.system.build) image;
    imageFile = "${hostConfiguration.system.image.id}_${hostConfiguration.system.image.version}.raw";
  };
in
{
  flake.nixosConfigurations.${hostName} = inputs.nixpkgs.lib.nixosSystem {
    system = hostSystem;
    modules = [
      ./modules/default.nix
      { nixpkgs.hostPlatform = hostSystem; }
    ];
  };

  flake.nixosConfigurations."${hostName}-installer" = installerOutput.hostConfiguration;

  perSystem =
    {
      system,
      pkgs,
      ...
    }:
    lib.optionalAttrs (system == hostSystem) {
      packages = {
        "${hostName}-image" = hostConfiguration.system.build.image;
        "${hostName}-update" = hostConfiguration.system.build.sysupdate-package;
        "${hostName}-installer" = installerOutput.package;
        run-image = pkgs.callPackage ./run-image.nix {
          inherit (hostConfiguration.system.build) image;
          imageFile = "${hostConfiguration.system.image.id}_${hostConfiguration.system.image.version}.raw";
        };
      };
    };
}
