# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  lib,
  runCommand,
}:
runCommand "molthouse-runtime-assets" { } ''
    mkdir -p "$out/share/molthouse"

    cat > "$out/share/molthouse/README" <<'EOF'
  MoltHouse runtime scaffolding

  This package stages the local MoltHouse runtime scaffold. Runtime configuration
  remains machine-local and will live under:

  - /var/lib/fleeti/molthouse
  - /run/fleeti/molthouse

  The actual QEMU runtime configuration, helper service, terminal/control
  plane, and guest boot assets arrive in later phases.
  EOF

      cat > "$out/share/molthouse/config.example.json" <<'EOF'
    {
      "shares": [],
      "notes": "Phase 2 scaffold. Runtime state is local to the machine and not generated from Fleeti profile JSON."
    }
    EOF
''
// {
  meta = {
    description = "MoltHouse runtime asset scaffold";
    platforms = lib.platforms.linux;
  };
}
