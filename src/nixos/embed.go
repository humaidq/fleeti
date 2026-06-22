/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package nixos

import "embed"

// Workspace contains embedded NixOS workspace files used for builds.
//
// The all: prefix is required so the underscore-prefixed _fleeti-tpm bundle
// (the self-contained Go module for the device TPM helper) is embedded too.
//go:embed flake.nix flake.lock flake-module.nix fleeti-installer.nix fleeti-installer.sh mk-fleeti-installer.nix sign-secure-boot.sh run-image.nix modules/*.nix modules/*.png packages/*.nix packages/*.py all:packages/_fleeti-tpm
var Workspace embed.FS
