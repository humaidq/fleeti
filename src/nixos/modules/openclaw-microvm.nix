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
  hostSystem = pkgs.stdenv.hostPlatform.system;
  networkConfigured = cfg.network.parentInterface != null && cfg.network.macAddress != null;
  macAddressPattern = "^([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}$";
  molthouseStateDir = "/var/lib/fleeti/molthouse";
  molthouseRuntimeDir = "/run/fleeti/molthouse";
  molthouseGuestMountPort = 10770;
  openclawMicrovmStateDir = "/var/lib/microvms/openclaw";
  openclawConsoleSocketPath = "${molthouseRuntimeDir}/console.sock";
  openclawQmpSocketPath = "${openclawMicrovmStateDir}/qmp.sock";
  molthousedPackage = pkgs.callPackage ../packages/molthoused.nix { };
  molthousectlPackage = pkgs.callPackage ../packages/molthousectl.nix { };
  molthouseManagerPackage = pkgs.callPackage ../packages/molthouse-manager.nix { };
  molthouseRuntimeAssetsPackage = pkgs.callPackage ../packages/molthouse-runtime-assets.nix { };
  openclawMountdPackage = pkgs.callPackage ../packages/openclaw-mountd.nix { };
  openclawRuntimeConfigDir = "/var/lib/openclaw/config";
  openclawRuntimeConfigPath = "${openclawRuntimeConfigDir}/openclaw.json";
  openclawRuntimeEnvPath = "${openclawRuntimeConfigDir}/openclaw-gateway.env";
  openclawGuestPkgs = import inputs.nixpkgs {
    system = hostSystem;
    overlays = [
      inputs.microvm.overlays.default
      inputs.openclaw.overlays.default
    ];
  };
  openclawExampleConfigFile = builtins.toFile "openclaw.example.json" (
    builtins.toJSON {
      gateway = {
        mode = "local";
        auth = {
          token = "replace-with-a-random-gateway-token";
        };
      };
      agent = {
        model = "anthropic/claude-sonnet-4";
      };
    }
  );
  openclawExampleEnvFile = builtins.toFile "openclaw-gateway.env.example" ''
    # Model provider credentials for the OpenClaw guest.
    ANTHROPIC_API_KEY=replace-me
    # OPENAI_API_KEY=replace-me
  '';
  openclawRuntimeReadmeFile = builtins.toFile "README" ''
    OpenClaw guest runtime configuration

    The guest starts with a default local-only OpenClaw config at:

    - ${openclawRuntimeConfigPath}
    - ${openclawRuntimeEnvPath} (optional)

    Suggested workflow:

    1. Review ${openclawRuntimeConfigPath} and adjust it for your deployment.
    2. Copy openclaw-gateway.env.example to openclaw-gateway.env and add secrets.
    3. If you want a clean starting point, compare against openclaw.example.json.
    4. Restart the guest service with: systemctl restart openclaw-gateway

    The OpenClaw microVM is built declaratively by Fleeti, but guest application
    configuration and secrets stay machine-local.
  '';
  openclawMacvtapInterface = {
    type = "macvtap";
    id = "openclaw0";
    mac = cfg.network.macAddress;
    macvtap = {
      link = cfg.network.parentInterface;
      mode = "bridge";
    };
  };
in
{
  options.fleeti.services.openclawMicrovm = {
    enable = lib.mkEnableOption "OpenClaw microVM";

    network.parentInterface = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      example = "enp1s0";
      description = "Host interface used for the OpenClaw macvtap uplink.";
    };

    network.macAddress = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      example = "02:00:00:00:00:01";
      description = "Guest MAC address for the OpenClaw microVM network interface.";
    };
  };

  config = lib.mkMerge [
    {
      assertions = [
        {
          assertion =
            (!cfg.enable) || (cfg.network.parentInterface == null) == (cfg.network.macAddress == null);
          message = "OpenClaw microVM networking requires both fleeti.services.openclawMicrovm.network.parentInterface and fleeti.services.openclawMicrovm.network.macAddress when either is set.";
        }
        {
          assertion =
            cfg.network.macAddress == null || builtins.match macAddressPattern cfg.network.macAddress != null;
          message = "OpenClaw microVM network MAC address must use the form 02:00:00:00:00:01.";
        }
      ];

      microvm.host.enable = lib.mkForce cfg.enable;
    }
    (lib.mkIf cfg.enable {
      services.dbus.packages = [ molthousedPackage ];

      environment.systemPackages = [
        molthousedPackage
        molthousectlPackage
        molthouseManagerPackage
      ];

      systemd.tmpfiles.rules = [
        "d ${molthouseStateDir} 0755 root root - -"
        "d ${molthouseRuntimeDir} 0775 root kvm - -"
      ];

      systemd.services.molthoused = {
        description = "MoltHouse privileged helper service";
        wantedBy = [ "multi-user.target" ];
        wants = [ "microvms.target" ];
        after = [
          "dbus.service"
          "microvms.target"
        ];
        environment = {
          MOLTHOUSE_ASSETS_DIR = "${molthouseRuntimeAssetsPackage}/share/molthouse";
          MOLTHOUSE_DBUS_BUS = "system";
          MOLTHOUSE_GUEST_HOME = "/home/fleeti";
          MOLTHOUSE_GUEST_MOUNT_PORT = toString molthouseGuestMountPort;
          MOLTHOUSE_GUEST_VSOCK_CID = "3";
          MOLTHOUSE_HOST_HOME = "/home/fleeti";
          MOLTHOUSE_QEMU = "${pkgs.qemu_kvm}/bin/qemu-system-x86_64";
          MOLTHOUSE_QMP_SOCKET = openclawQmpSocketPath;
          MOLTHOUSE_RUNTIME_DIR = molthouseRuntimeDir;
          MOLTHOUSE_STATE_DIR = molthouseStateDir;
          MOLTHOUSE_SYSTEMCTL = "${pkgs.systemd}/bin/systemctl";
          MOLTHOUSE_VIRTIOFSD = "${pkgs.virtiofsd}/bin/virtiofsd";
          MOLTHOUSE_VM_BACKEND = "systemd-service";
          MOLTHOUSE_VM_SYSTEMD_SERVICE = "microvm@openclaw.service";
        };
        serviceConfig = {
          ExecStartPre = "${molthousedPackage}/bin/molthoused ensure-state";
          ExecStart = "${molthousedPackage}/bin/molthoused serve";
          BusName = "ae.fleeti.MoltHouse1";
          Restart = "on-failure";
          RestartSec = "5s";
          Type = "dbus";
        };
      };

      microvm.vms.openclaw = {
        autostart = true;
        pkgs = openclawGuestPkgs;

        config =
          {
            config,
            lib,
            pkgs,
            ...
          }:
          {
            imports = [
              inputs.openclaw.nixosModules.openclaw-gateway
            ];

            services.openclaw-gateway = {
              enable = true;
              package = pkgs.openclaw-gateway;
              stateDir = "/var/lib/openclaw";
              environment = {
                OPENCLAW_CONFIG_PATH = openclawRuntimeConfigPath;
                CLAWDBOT_CONFIG_PATH = openclawRuntimeConfigPath;
              };
              environmentFiles = [ "-${openclawRuntimeEnvPath}" ];
            };

            networking.hostName = "openclaw";
            networking.useNetworkd = true;

            services.getty.autologinUser = "fleeti";

            systemd.network.enable = true;
            systemd.network.networks."20-uplink" = {
              matchConfig.Type = "ether";
              networkConfig = {
                DHCP = "yes";
                IPv6AcceptRA = true;
              };
              linkConfig.RequiredForOnline = "routable";
            };

            systemd.tmpfiles.rules = [
              "d ${openclawRuntimeConfigDir} 0750 openclaw openclaw - -"
              "d /var/lib/openclaw/secrets 0750 openclaw openclaw - -"
            ];

            systemd.services.openclaw-bootstrap = {
              description = "Seed OpenClaw guest runtime examples";
              wantedBy = [ "multi-user.target" ];
              before = [ "openclaw-gateway.service" ];
              after = [ "local-fs.target" ];
              serviceConfig.Type = "oneshot";
              script = ''
                install -d -m 0750 -o openclaw -g openclaw ${openclawRuntimeConfigDir}
                install -d -m 0750 -o openclaw -g openclaw /var/lib/openclaw/secrets

                if [ ! -e ${openclawRuntimeConfigDir}/README ]; then
                  install -m 0640 -o openclaw -g openclaw ${openclawRuntimeReadmeFile} ${openclawRuntimeConfigDir}/README
                fi

                if [ ! -e ${openclawRuntimeConfigDir}/openclaw.example.json ]; then
                  install -m 0640 -o openclaw -g openclaw ${openclawExampleConfigFile} ${openclawRuntimeConfigDir}/openclaw.example.json
                fi

                if [ ! -e ${openclawRuntimeConfigPath} ]; then
                  install -m 0640 -o openclaw -g openclaw ${openclawExampleConfigFile} ${openclawRuntimeConfigPath}
                fi

                if [ ! -e ${openclawRuntimeConfigDir}/openclaw-gateway.env.example ]; then
                  install -m 0640 -o openclaw -g openclaw ${openclawExampleEnvFile} ${openclawRuntimeConfigDir}/openclaw-gateway.env.example
                fi
              '';
            };

            systemd.services.openclaw-gateway = {
              after = [ "openclaw-bootstrap.service" ];
              requires = [ "openclaw-bootstrap.service" ];
              unitConfig.ConditionPathExists = openclawRuntimeConfigPath;
            };

            systemd.services.openclaw-mountd = {
              description = "OpenClaw guest mount helper";
              wantedBy = [ "multi-user.target" ];
              after = [ "local-fs.target" ];
              serviceConfig = {
                ExecStart = "${openclawMountdPackage}/bin/openclaw-mountd";
                Environment = [
                  "OPENCLAW_MOUNTD_USER=fleeti"
                  "OPENCLAW_MOUNTD_PORT=${toString molthouseGuestMountPort}"
                ];
                Restart = "on-failure";
                RestartSec = "2s";
              };
            };

            microvm = {
              hypervisor = "qemu";
              # MoltHouse attaches to the guest's serial console through a
              # machine-local socket instead of a login prompt.
              qemu.serialConsole = false;
              qemu.extraArgs = [
                "-chardev"
                "socket,id=console0,path=${openclawConsoleSocketPath},server=on,wait=off"
                "-serial"
                "chardev:console0"
              ];
              kernelParams = [
                "console=ttyS0"
                "earlyprintk=ttyS0"
              ];
              preStart = ''
                rm -f ${openclawConsoleSocketPath}
              '';
              vsock.cid = 3;
              mem = 2049;
              vcpu = 1;
              socket = openclawQmpSocketPath;
              # Avoid executing guest binaries from a host-backed virtiofs store.
              storeOnDisk = true;
              storeDiskType = "erofs";
              storeDiskErofsFlags = [
                "-zlz4hc"
                "-Eztailpacking"
              ];
              interfaces = lib.optional networkConfigured openclawMacvtapInterface;
              volumes = [
                {
                  image = "openclaw-state.img";
                  mountPoint = "/var/lib/openclaw";
                  size = 8192;
                }
              ];
            };

            environment.systemPackages = [
              pkgs.openclaw-gateway
            ];

            users.mutableUsers = false;
            users.users.fleeti = {
              isNormalUser = true;
              description = "OpenClaw guest user";
              initialPassword = "fleeti";
              extraGroups = [ "wheel" ];
            };

            system.stateVersion = "24.11";
          };
      };
    })
  ];
}
