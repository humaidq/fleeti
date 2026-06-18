# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  lib,
  pkgs,
  buildCommit ? "unknown",
  ...
}:

pkgs.buildGoModule rec {
  pname = "fleeti";
  version = "v0.2.0";

  src = ./.;

  # use vendor has null to avoid creating a Fixed-Output derivation
  # if using the devshell the go-update will ensure that
  # `go mod vendor` is run to keep the vendor directory up to date
  # this is tracked so it will give the reproducibility of the build
  vendorHash = null;

  ldflags = [
    "-X github.com/humaidq/fleeti/v2/cmd.BuildVersion=${version}"
    "-X github.com/humaidq/fleeti/v2/cmd.BuildCommit=${buildCommit}"
  ];

  nativeBuildInputs = [ pkgs.makeWrapper ];

  # Tools the running service shells out to at runtime. Wrapping them onto the
  # binary's PATH makes fleeti self-contained regardless of how it is launched
  # (systemd service, `nix run`, etc.).
  postFixup = ''
    wrapProgram "$out/bin/fleeti" \
      --prefix PATH : "${lib.makeBinPath [
        pkgs.nix-search-cli
        # Post-build Secure Boot signing (sign-secure-boot.sh runs outside Nix).
        pkgs.bash
        pkgs.coreutils
        pkgs.sbsigntool # sbsign, sbverify
        pkgs.efitools # cert-to-efi-sig-list, sign-efi-sig-list
        pkgs.mtools # mcopy, mdir, mmd
        pkgs.util-linux # sfdisk
        pkgs.jq
        pkgs.xz
      ]}"
  '';
}
