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
        Should include BOOTSTRAP_TOKEN, WebAuthn settings, and any optional OpenRouter AI wizard settings.
      '';
    };
  };

  config = mkIf cfg.enable {
    services.postgresql = {
      enable = true;

      ensureDatabases = [ "fleeti" ];

      ensureUsers = [
        {
          name = "fleeti-service";
        }
      ];
    };

    users.users.fleeti-service = {
      isSystemUser = true;
      group = "fleeti-service";
      description = "Fleeti service user";
    };

    users.groups.fleeti-service = { };

    # The PostgreSQL module only supports ensureDBOwnership when the
    # database name matches the role name, so set Fleeti's DB owner explicitly.
    systemd.services.postgresql-setup.script = mkAfter ''
      psql -d postgres -tAc 'ALTER DATABASE "fleeti" OWNER TO "fleeti-service";'
    '';

    systemd.services.fleeti = {
      description = "Fleeti Service";
      wantedBy = [ "multi-user.target" ];
      after = [
        "network.target"
        "postgresql.service"
        "postgresql-setup.service"
      ];
      requires = [
        "postgresql.service"
        "postgresql-setup.service"
      ];
      path = [
        pkgs.git
        pkgs.nix
        # The Secure Boot signing toolchain (sbsign, efitools, mtools, sfdisk,
        # jq, xz) is wrapped onto the fleeti binary's PATH in src/default.nix,
        # so it no longer needs to be listed here.
      ];

      serviceConfig = {
        Type = "simple";
        User = "fleeti-service";
        Group = "fleeti-service";
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
          "HOME=/var/lib/fleeti"
          "XDG_CACHE_HOME=/var/lib/fleeti/.cache"
        ];
      };

      script = ''
        exec ${pkgs.fleeti}/bin/fleeti start --port ${toString cfg.port}
      '';
    };
  };
}
