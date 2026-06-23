# Copyright 2026 Humaid Alqasimi
# SPDX-License-Identifier: Apache-2.0
#
# TEMPORARY boot-diagnostics module. Makes the boot observable so a freeze can be
# pinpointed: turns the plymouth splash off and prints systemd/kernel status to the
# screen (and the QEMU serial, if used). Remove the import in modules/default.nix and
# delete this file once the failing unit is identified.
#
# Because the appliance's UKI cmdline is baked and signed, kernel params cannot be edited
# at the boot menu — these have to be built into the image (rebuild + re-sign + redeploy).
{
  lib,
  ...
}:
{
  # image.nix forces plymouth on with mkForce (priority 50); override with a
  # lower priority number so the splash is off and boot text is visible.
  boot.plymouth.enable = lib.mkOverride 40 false;

  # Show per-unit status ("A start job is running for <unit>...") in both initrd and the
  # main system, and raise the kernel log level so the last message before a freeze shows.
  boot.kernelParams = [
    "systemd.show_status=1"
    "rd.systemd.show_status=1"
    "systemd.log_level=debug"
    "systemd.log_target=console"
  ];
  boot.consoleLogLevel = lib.mkForce 7;
}
