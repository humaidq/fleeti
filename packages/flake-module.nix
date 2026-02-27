# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{ self, ... }:
let
  buildCommit =
    if self ? shortRev then
      self.shortRev
    else if self ? rev then
      builtins.substring 0 7 self.rev
    else if self ? dirtyShortRev then
      self.dirtyShortRev
    else if self ? dirtyRev then
      builtins.substring 0 7 self.dirtyRev
    else
      "unknown";
in
{
  perSystem =
    {
      pkgs,
      ...
    }:
    let
      inherit (pkgs) callPackage;
      docsPackage = callPackage ../docs { };
      fleetiPackage = callPackage ../src {
        inherit buildCommit;
      };
    in
    {
      packages = {
        default = fleetiPackage;
        fleeti = fleetiPackage;
        fleeti-docs = docsPackage;
        docs = docsPackage;
        doc = docsPackage;
      };
    };
}
