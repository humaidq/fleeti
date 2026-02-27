/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package nixos

import "embed"

// Workspace contains embedded NixOS workspace files used for builds.
//
//go:embed flake.nix flake.lock flake-module.nix fleeti-installer.nix fleeti-installer.sh mk-fleeti-installer.nix run-image.nix modules/*.nix modules/*.png
var Workspace embed.FS
