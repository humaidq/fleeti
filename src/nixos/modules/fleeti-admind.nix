# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.fleeti.services.admind;
  agentPackage = pkgs.callPackage ../packages/fleeti-admind.nix { };
  tpmHelperPackage = pkgs.callPackage ../packages/fleeti-tpm.nix { };
  stateDir = "/var/lib/fleeti/admind";
  runtimeDir = "/run/fleeti/admind";
in
{
  options.fleeti.services.admind = {
    enable = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Run the Fleeti device management agent.";
    };

    fleetId = lib.mkOption {
      type = lib.types.str;
      default = "";
      description = "Fleet UUID baked into the image at build time. Empty disables the agent.";
    };

    serverUrl = lib.mkOption {
      type = lib.types.str;
      default = "";
      description = "Fleeti instance base URL the agent reports to. Empty disables the agent.";
    };

    telemetryIntervalSeconds = lib.mkOption {
      type = lib.types.int;
      default = 60;
      description = "How often the agent reports telemetry, in seconds.";
    };
  };

  config = lib.mkIf cfg.enable {
    environment.systemPackages = [ agentPackage ];

    # Remote attestation reads the TPM through the in-kernel resource manager
    # (/dev/tpmrm0). Devices without a TPM simply skip attestation.
    security.tpm2.enable = lib.mkDefault true;

    # Once a TPM is present the UKI is measured, which flips systemd's
    # ConditionSecurity=measured-uki true and activates its measured-boot/TPM2 setup
    # units (SRK generation + PCR-phase extension) in early boot. On this appliance they
    # run Before=sysinit.target/basic.target and stall there, freezing the boot at the
    # plymouth splash. Fleeti attestation uses its own attestation key (fleeti-tpm) and
    # quotes PCRs directly, so none of these are needed; mask the whole family.
    systemd.suppressedSystemUnits = [
      "systemd-tpm2-setup-early.service"
      "systemd-tpm2-setup.service"
      "systemd-pcrphase-sysinit.service"
      "systemd-pcrphase.service"
      "systemd-pcrmachine.service"
      "systemd-pcrnvdone.service"
      "systemd-pcrproduct.service"
    ];
    boot.initrd.systemd.suppressedUnits = [
      "systemd-pcrphase-initrd.service"
    ];

    systemd.services.fleeti-admind = {
      description = "Fleeti device management agent";
      wantedBy = [ "multi-user.target" ];
      wants = [ "network.target" ];
      after = [ "network.target" ];

      environment = {
        FLEETI_ADMIND_FLEET_ID = cfg.fleetId;
        FLEETI_ADMIND_SERVER_URL = cfg.serverUrl;
        FLEETI_ADMIND_STATE_DIR = stateDir;
        FLEETI_ADMIND_RUNTIME_DIR = runtimeDir;
        FLEETI_ADMIND_TELEMETRY_INTERVAL = toString cfg.telemetryIntervalSeconds;
        FLEETI_SYSTEMD_SYSUPDATE = "${pkgs.systemd}/lib/systemd/systemd-sysupdate";
        FLEETI_SYSTEMCTL = "${pkgs.systemd}/bin/systemctl";
        FLEETI_TPM_HELPER = "${tpmHelperPackage}/bin/fleeti-tpm";
      };

      serviceConfig = {
        ExecStart = "${agentPackage}/bin/fleeti-admind serve";
        Restart = "on-failure";
        RestartSec = "10s";

        # systemd creates and owns these; RuntimeDirectory is world-readable so the
        # Fleeti Admin GUI (running as the fleeti user) can read status.json.
        StateDirectory = "fleeti/admind";
        StateDirectoryMode = "0700";
        RuntimeDirectory = "fleeti/admind";
        RuntimeDirectoryMode = "0755";
        RuntimeDirectoryPreserve = "yes";
      };
    };
  };
}
