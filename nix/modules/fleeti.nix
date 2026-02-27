# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  config,
  lib,
  pkgs,
  ...
}:

with lib;

let
  cfg = config.services.fleeti;
in
{
  options.services.fleeti = {
    enable = mkEnableOption "Fleeti service";

    port = mkOption {
      type = types.port;
      default = 8080;
      description = "Port for the web interface to listen on";
    };

    envFile = mkOption {
      type = types.path;
      description = ''
        Path to environment file containing secrets.
        Should include BOOTSTRAP_TOKEN and WebAuthn settings.
      '';
    };
  };

  config = mkIf cfg.enable {
    services.postgresql = {
      enable = true;

      ensureDatabases = [ "fleeti" ];

      ensureUsers = [
        {
          name = "fleeti";
          ensureDBOwnership = true;
        }
      ];
    };

    users.users.fleeti = {
      isSystemUser = true;
      group = "fleeti";
      description = "Fleeti service user";
    };

    users.groups.fleeti = { };

    systemd.services.fleeti = {
      description = "Fleeti Service";
      wantedBy = [ "multi-user.target" ];
      after = [
        "network.target"
        "postgresql.service"
      ];
      requires = [ "postgresql.service" ];
      path = [
        pkgs.nix
      ];

      serviceConfig = {
        Type = "simple";
        User = "fleeti";
        Group = "fleeti";
        Restart = "on-failure";
        RestartSec = "5s";

        StateDirectory = "fleeti";
        WorkingDirectory = "/var/lib/fleeti";

        EnvironmentFile = cfg.envFile;

        NoNewPrivileges = true;
        PrivateTmp = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        ReadWritePaths = [ "/var/lib/fleeti" ];

        Environment = [
          "DATABASE_URL=postgres:///fleeti"
        ];
      };

      script = ''
        exec ${pkgs.fleeti}/bin/fleeti start --port ${toString cfg.port}
      '';
    };
  };
}
