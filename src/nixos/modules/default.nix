# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  config,
  inputs,
  modulesPath,
  pkgs,
  lib,
  ...
}:

let
  fleetiSbEnrollPackage = pkgs.callPackage ../packages/fleeti-sb-enroll.nix { };
  fleetiAdmindPackage = pkgs.callPackage ../packages/fleeti-admind.nix { };
  fleetiAdminPackage = pkgs.callPackage ../packages/fleeti-admin.nix {
    sudoPath = "${config.security.wrapperDir}/sudo";
    sbEnrollPath = "${fleetiSbEnrollPackage}/bin/fleeti-sb-enroll";
    admindPath = "${fleetiAdmindPackage}/bin/fleeti-admind";
  };
in
{
  imports = [
    (modulesPath + "/profiles/image-based-appliance.nix")
    inputs.microvm.nixosModules.host
    ./filesystems.nix
    ./image.nix
    ./overlays.nix
    ./build-overrides.nix
    ./openclaw-microvm.nix
    ./fleeti-admind.nix
    ./fleeti-update.nix
    ./update.nix
    ./update-package.nix
  ];

  boot.loader.grub.enable = false;

  networking.hostName = "fleeti";
  networking.networkmanager.enable = true;

  hardware.graphics.enable = true;
  hardware.enableRedistributableFirmware = true;

  time.timeZone = "UTC";
  i18n.defaultLocale = "en_US.UTF-8";

  services.openssh.enable = true;

  programs.labwc.enable = true;

  environment.etc."xdg/labwc/autostart".text = ''
    ${pkgs.swaybg}/bin/swaybg -i ${./bg.png} -m fill &
    ${pkgs.sfwbar}/bin/sfwbar &
  '';

  environment.etc."xdg/sfwbar/sfwbar.config".text = ''
    #Api2

    include "winops.widget"

    bar {
      edge = "bottom"
      layer = "top"
      mirror = "*"
      exclusive_zone = "auto"

      widget "startmenu.widget"

      taskbar {
        rows = 1;
        tooltips = true;
        icons = true;
        labels = true;
        sort = false;
        action[RightClick] = Menu("winops");
        action[Drag] = Focus();
      }

      label {
        css = "* { -GtkWidget-hexpand: true; }"
      }

      tray {
        rows = 1;
      }

      widget "clock.widget" {
        disable = false;
        time_format = "%H:%M";
        tooltip_format = "%H:%M\n%x";
        week_starts_on_sunday = false;
        reset_on_popup = false;
      }
    }

    #hidden {
      -GtkWidget-visible: false;
    }

    button,
    button image {
      min-height: 0px;
      outline-style: none;
      box-shadow: none;
      background-image: none;
      border-image: none;
    }

    label {
      font-family: Sans;
      font-size: calc(@bar_thickness * 0.7);
    }

    image {
      -ScaleImage-symbolic: true;
    }

    window#sfwbar {
      background-color: rgba(0, 0, 0, 0.55);
    }

    .module,
    button#startmenu,
    button#module {
      border: none;
      padding: calc(@bar_thickness * 0.1);
      margin: 0px;
      -GtkWidget-vexpand: true;
    }

    .module:hover,
    button#startmenu:hover,
    button#module:hover {
      background-color: rgba(213, 213, 213, 0.25);
    }

    .module image,
    button#startmenu image,
    button#module image {
      padding: 0px;
      margin: 0px;
      min-width: calc(@bar_thickness * 0.8);
      min-height: calc(@bar_thickness * 0.8);
      -GtkWidget-valign: center;
      -GtkWidget-vexpand: true;
      color: @theme_fg_color;
    }

    button#taskbar_item {
      padding: calc(@bar_thickness * 0.1);
      border-radius: 0px;
      border-width: 0px;
      background-color: transparent;
    }

    button#taskbar_item:hover {
      background-color: rgba(213, 213, 213, 0.25);
    }

    button#taskbar_item image {
      min-height: calc(@bar_thickness * 0.8);
      min-width: calc(@bar_thickness * 0.8);
      padding-right: calc(@bar_thickness * 0.25);
      padding-left: calc(@bar_thickness * 0.25);
      -ScaleImage-symbolic: false;
      -GtkWidget-vexpand: true;
    }

    button#tray_item {
      margin: 0px;
      border: none;
      padding: 0px;
    }

    button#tray_item.passive {
      -GtkWidget-visible: false;
    }

    button#tray_item image {
      -GtkWidget-valign: center;
      -GtkWidget-vexpand: true;
      padding: 3px;
      margin: 0px;
      border: none;
    }

    #app_menu_system #menu_item image {
      -ScaleImage-symbolic: false;
    }

    #menu_item,
    #menu_item image,
    #menu_item label {
      -GtkWidget-halign: start;
    }

    menuitem image {
      min-width: 16px;
      min-height: 16px;
      padding-right: 2px;
    }
  '';

  services.greetd = {
    enable = true;
    restart = true;
    settings = rec {
      initial_session = {
        user = "fleeti";
        command = "${pkgs.dbus}/bin/dbus-run-session ${pkgs.labwc}/bin/labwc";
      };
      default_session = initial_session;
    };
  };

  users.users.fleeti = {
    isNormalUser = true;
    description = "Fleeti login user";
    initialPassword = "fleeti";
    extraGroups = [
      "networkmanager"
      "wheel"
    ];
  };

  security.sudo = {
    enable = true;
    extraRules = [
      {
        users = [ "fleeti" ];
        commands = [
          {
            command = "${pkgs.systemd}/lib/systemd/systemd-sysupdate";
            options = [ "NOPASSWD" ];
          }
          {
            command = "${pkgs.systemd}/bin/systemctl reboot";
            options = [ "NOPASSWD" ];
          }
          {
            command = "${pkgs.systemd}/bin/systemctl reboot --firmware-setup";
            options = [ "NOPASSWD" ];
          }
          {
            command = "${fleetiSbEnrollPackage}/bin/fleeti-sb-enroll";
            options = [ "NOPASSWD" ];
          }
          {
            # Lets the Admin GUI hand a local update to the daemon (the single updater).
            command = "${fleetiAdmindPackage}/bin/fleeti-admind request-update";
            options = [ "NOPASSWD" ];
          }
        ];
      }
    ];
  };

  environment.systemPackages = with pkgs; [
    fleetiAdminPackage
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
