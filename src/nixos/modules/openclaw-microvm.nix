# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  config,
  inputs,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.fleeti.services.openclawMicrovm;
  openclawGatewayPackage =
    inputs.openclaw.packages.${pkgs.stdenv.hostPlatform.system}.openclaw-gateway;
in
{
  imports = [
    inputs.openclaw.nixosModules.openclaw-gateway
    inputs.microvm.nixosModules.host
  ];

  options.fleeti.services.openclawMicrovm = {
    enable = lib.mkEnableOption "OpenClaw gateway in a cloud-hypervisor MicroVM";

    hostInterface = lib.mkOption {
      type = lib.types.str;
      default = "eth0";
      description = "Host network interface for the OpenClaw MicroVM macvtap uplink.";
    };
  };

  config = lib.mkMerge [
    {
      microvm.host.enable = lib.mkDefault false;
    }

    (lib.mkIf cfg.enable {
      microvm.host.enable = true;

      microvm.vms.openclaw = {
        config = {
          imports = [
            inputs.openclaw.nixosModules.openclaw-gateway
          ];

          networking.hostName = "openclaw";
          networking.useNetworkd = true;

          systemd.network.enable = true;
          systemd.network.networks."10-uplink" = {
            matchConfig.Type = "ether";
            networkConfig.DHCP = "yes";
          };

          services.openclaw-gateway = {
            enable = true;
            package = openclawGatewayPackage;
            config = {
              gateway = {
                mode = "local";
                bind = "loopback";
                auth = {
                  mode = "token";
                  token = "fleeti-openclaw-token";
                };
              };
            };
          };

          microvm = {
            hypervisor = "cloud-hypervisor";

            shares = [
              {
                source = "/nix/store";
                mountPoint = "/nix/.ro-store";
                tag = "ro-store";
                proto = "virtiofs";
              }
            ];

            interfaces = [
              {
                type = "macvtap";
                id = "mv-openclaw0";
                mac = "02:00:00:00:10:01";
                macvtap = {
                  link = cfg.hostInterface;
                  mode = "bridge";
                };
              }
            ];

            volumes = [
              {
                image = "openclaw-var.img";
                mountPoint = "/var";
                size = 4096;
              }
            ];
          };

          system.stateVersion = "24.11";
        };
      };
    })
  ];
}
