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
  openclawHostName = "${hostName}-openclaw";
  mkHostConfiguration =
    extraModules:
    inputs.nixpkgs.lib.nixosSystem {
      system = hostSystem;
      specialArgs = {
        inherit inputs;
      };
      modules = [
        ./modules/default.nix
        { nixpkgs.hostPlatform = hostSystem; }
      ]
      ++ extraModules;
    };
  hostConfiguration = config.flake.nixosConfigurations.${hostName}.config;
  openclawHostConfiguration = config.flake.nixosConfigurations.${openclawHostName}.config;
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
  flake.nixosConfigurations.${hostName} = mkHostConfiguration [ ];

  # Test-only OpenClaw outputs. Production builds and updates should continue to use
  # the default `fleeti` configuration, with the web UI boolean only controlling
  # whether the OpenClaw stack is enabled in that build.
  flake.nixosConfigurations.${openclawHostName} = mkHostConfiguration [
    {
      fleeti.services.openclawMicrovm.enable = true;
      system.image.id = openclawHostName;
    }
  ];

  flake.nixosConfigurations."${hostName}-installer" = installerOutput.hostConfiguration;

  perSystem =
    {
      system,
      pkgs,
      ...
    }:
    let
      fleetiAdminPackage = pkgs.callPackage ./packages/fleeti-admin.nix {
        sudoPath = "${hostConfiguration.security.wrapperDir}/sudo";
      };
      fleetiAdmindPackage = pkgs.callPackage ./packages/fleeti-admind.nix { };
      molthousedPackage = pkgs.callPackage ./packages/molthoused.nix { };
      molthousectlPackage = pkgs.callPackage ./packages/molthousectl.nix { };
      molthouseManagerPackage = pkgs.callPackage ./packages/molthouse-manager.nix { };
      molthouseRuntimeAssetsPackage = pkgs.callPackage ./packages/molthouse-runtime-assets.nix { };
      openclawImageFile = "${openclawHostConfiguration.system.image.id}_${openclawHostConfiguration.system.image.version}.raw";
    in
    lib.optionalAttrs (system == hostSystem) {
      packages = {
        "${hostName}-image" = hostConfiguration.system.build.image;
        "${hostName}-update" = hostConfiguration.system.build.sysupdate-package;
        "${hostName}-installer" = installerOutput.package;
        # Test-only OpenClaw build outputs.
        "${openclawHostName}-image" = openclawHostConfiguration.system.build.image;
        "${openclawHostName}-update" = openclawHostConfiguration.system.build.sysupdate-package;
        "${openclawHostName}-runner" =
          openclawHostConfiguration.microvm.vms.openclaw.config.config.microvm.declaredRunner;
        fleeti-admin = fleetiAdminPackage;
        fleeti-admind = fleetiAdmindPackage;
        molthoused = molthousedPackage;
        molthousectl = molthousectlPackage;
        molthouse-manager = molthouseManagerPackage;
        molthouse-runtime-assets = molthouseRuntimeAssetsPackage;
        run-image = pkgs.callPackage ./run-image.nix {
          inherit (hostConfiguration.system.build) image;
          imageFile = "${hostConfiguration.system.image.id}_${hostConfiguration.system.image.version}.raw";
        };
        "${openclawHostName}-run-image" = pkgs.callPackage ./run-image.nix {
          inherit (openclawHostConfiguration.system.build) image;
          imageFile = openclawImageFile;
        };
      };
    };
}
