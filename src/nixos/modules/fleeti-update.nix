# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.fleeti.services.update;
  admindCfg = config.fleeti.services.admind;

  updatePackage = pkgs.callPackage ../packages/fleeti-update.nix { };

  inherit (config.system.image) id;
  ukiName = config.boot.uki.name;

  stagingDir = "/var/cache/fleeti-update";
  definitionsDir = "/etc/fleeti/sysupdate-local";

  serverUrl = lib.removeSuffix "/" admindCfg.serverUrl;
  baseURL = "${serverUrl}/update/${admindCfg.fleetId}/";
  storeURL = "${serverUrl}/update/castr/";

  # Local-source sysupdate transfer definitions. They mirror the network
  # transfers in update.nix but read the locally reconstructed, zstd-compressed
  # artifacts from the staging directory, so systemd-sysupdate still owns the A/B
  # apply (slot selection, partition relabel, UKI install, rollback) while
  # fleeti-update only replaces the transport.
  nixStoreTransfer = ''
    [Source]
    Type=regular-file
    Path=${stagingDir}
    MatchPattern=${id}_@v.nix-store.raw.zst

    [Target]
    Type=partition
    Path=auto
    MatchPattern=nix-store_@v
    MatchPartitionType=linux-generic
    InstancesMax=2
    ReadOnly=yes

    [Transfer]
    ProtectVersion=%A
    Verify=no
  '';

  bootImageTransfer = ''
    [Source]
    Type=regular-file
    Path=${stagingDir}
    MatchPattern=${ukiName}_@v.efi.zst

    [Target]
    Type=regular-file
    Path=/EFI/Linux
    PathRelativeTo=boot
    MatchPattern=${ukiName}_@v.efi
    Mode=0444
    InstancesMax=2

    [Transfer]
    ProtectVersion=%A
    Verify=no
  '';
in
{
  options.fleeti.services.update = {
    enable = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Enable Fleeti delta (binary-patch) updates via fleeti-update.";
    };
  };

  config = lib.mkIf cfg.enable {
    environment.systemPackages = [ updatePackage ];

    environment.etc."fleeti/sysupdate-local/10-nix-store.transfer".text = nixStoreTransfer;
    environment.etc."fleeti/sysupdate-local/20-boot-image.transfer".text = bootImageTransfer;

    # Root-owned staging directory for the reconstructed, compressed artifacts.
    systemd.tmpfiles.rules = [
      "d ${stagingDir} 0700 root root -"
    ];

    # fleeti-admind runs as root and invokes fleeti-update; the child inherits
    # this environment. Delta updates are attempted first, with the existing
    # full-download path as the fallback.
    systemd.services.fleeti-admind.environment = lib.mkIf admindCfg.enable {
      FLEETI_UPDATE = "${updatePackage}/bin/fleeti-update";
      FLEETI_UPDATE_BASE_URL = baseURL;
      FLEETI_UPDATE_STORE_URL = storeURL;
      FLEETI_UPDATE_IMAGE_ID = id;
      FLEETI_UPDATE_UKI_NAME = ukiName;
      FLEETI_UPDATE_STAGING_DIR = stagingDir;
      FLEETI_UPDATE_DEFINITIONS_DIR = definitionsDir;
      FLEETI_DESYNC = "${pkgs.desync}/bin/desync";
      FLEETI_ZSTD = "${pkgs.zstd}/bin/zstd";
    };
  };
}
