# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
#
# Device-side TPM attestation helper. Built from the same Go module as the Fleeti
# server but as its own small binary so managed devices do not pull in the whole
# server. The Fleeti device agent shells out to it to create the attestation key
# and produce signed PCR quotes.
{
  lib,
  buildGoModule,
}:
buildGoModule {
  pname = "fleeti-tpm";
  version = "v0.2.0";

  src = ../../.;

  # Matches src/default.nix: vendoring is kept in-tree, so no fixed-output hash.
  vendorHash = null;

  subPackages = [ "cmd/fleeti-tpm" ];

  meta = {
    description = "Fleeti device TPM attestation helper";
    mainProgram = "fleeti-tpm";
    platforms = lib.platforms.linux;
  };
}
