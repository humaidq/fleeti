# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  perSystem =
    {
      pkgs,
      ...
    }:
    let
      inherit (pkgs) callPackage;
      docsPackage = callPackage ../docs { };
    in
    {
      packages = {
        default = callPackage ../src { };
        fleeti = callPackage ../src { };
        fleeti-docs = docsPackage;
        docs = docsPackage;
        doc = docsPackage;
      };
    };
}
