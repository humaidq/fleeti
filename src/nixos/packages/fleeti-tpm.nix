# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
#
# Device-side TPM attestation helper: a small standalone binary so managed
# devices do not pull in the whole server. Its source is a self-contained Go
# module bundled in packages/_fleeti-tpm/ (see the src note below). The Fleeti
# device agent shells out to it to create the attestation key and produce signed
# PCR quotes.
{
  lib,
  buildGoModule,
}:
# NOTE: keep vendorHash below in sync with go.mod.embed/go.sum. Regenerate with
# `vendorHash = lib.fakeHash;` then copy the hash Nix reports.
buildGoModule {
  pname = "fleeti-tpm";
  version = "v0.2.0";

  # Self-contained Go module bundled alongside this file so it builds both in the
  # repo flake and in the generated forge flake (whose root is src/nixos/). It is
  # an underscore-prefixed directory, so the parent Go module's tooling ignores
  # it, and its go.mod is shipped as go.mod.embed (restored below) so //go:embed
  # does not treat it as a nested module. See packages/_fleeti-tpm/.
  src = ./_fleeti-tpm;

  postPatch = ''
    cp go.mod.embed go.mod
  '';

  # Pinned, content-addressed dependency hash (just go-tpm + its x/sys dep). This
  # keeps the bundle to a few files instead of vendoring a large x/sys tree.
  vendorHash = "sha256-lUZeAZITnWwiTlOcSznOATKlTlUFyDf8fvaSmuhAeVc=";

  subPackages = [ "." ];

  meta = {
    description = "Fleeti device TPM attestation helper";
    mainProgram = "fleeti-tpm";
    platforms = lib.platforms.linux;
  };
}
