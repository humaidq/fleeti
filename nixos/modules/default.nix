# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  modulesPath,
  pkgs,
  lib,
  ...
}:

{
  imports = [
    (modulesPath + "/profiles/image-based-appliance.nix")
    ./filesystems.nix
    ./image.nix
    ./build-overrides.nix
    ./update.nix
    ./update-package.nix
  ];

  boot.loader.grub.enable = false;

  networking.hostName = "fleeti";
  networking.networkmanager.enable = true;

  time.timeZone = "UTC";
  i18n.defaultLocale = "en_US.UTF-8";

  services.openssh.enable = true;

  programs.labwc.enable = true;

  environment.etc."xdg/labwc/autostart".text = ''
    ${pkgs.swaybg}/bin/swaybg -i ${./bg.png} -m fill &
    ${pkgs.sfwbar}/bin/sfwbar &
  '';

  services.greetd = {
    enable = true;
    restart = true;
    settings = rec {
      initial_session = {
        user = "root";
        command = "${pkgs.dbus}/bin/dbus-run-session ${pkgs.labwc}/bin/labwc";
      };
      default_session = initial_session;
    };
  };

  users.users.root.initialPassword = "";

  environment.systemPackages = with pkgs; [
    foot
    git
    sfwbar
    wget
    vim
  ];

  nix.settings.experimental-features = [
    "nix-command"
    "flakes"
  ];

  system.image.version = lib.mkDefault "1";

  system.stateVersion = "24.11";
}
