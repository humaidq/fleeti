# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
{
  buildNpmPackage,
  nodejs,
  pkg-config,
  vips,
  ...
}:

buildNpmPackage {
  pname = "fleeti-docs";
  version = "0.0.1";
  src = ./.;

  inherit nodejs;

  buildInputs = [
    vips
  ];

  nativeBuildInputs = [
    pkg-config
  ];

  # Tell sharp to use system libvips instead of building from source.
  SHARP_IGNORE_GLOBAL_LIBVIPS = "1";

  npmDepsHash = "sha256-YHx0j49BuuQWrfv2vPmNjRas/tXKlGPJY7BV+qkZij8=";

  installPhase = ''
    runHook preInstall
    mkdir -p $out
    cp -pr --reflink=auto dist/. $out/
    runHook postInstall
  '';
}
