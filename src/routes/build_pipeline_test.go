/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectInstallerArtifactsRecursesIntoResultTree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nestedDir := filepath.Join(root, "iso")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("failed to create nested directory: %v", err)
	}

	artifactPath := filepath.Join(nestedDir, "fleeti-installer.iso")
	if err := os.WriteFile(artifactPath, []byte("installer"), 0o644); err != nil {
		t.Fatalf("failed to create installer artifact: %v", err)
	}

	artifacts, err := collectInstallerArtifacts(root)
	if err != nil {
		t.Fatalf("collectInstallerArtifacts returned error: %v", err)
	}

	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(artifacts))
	}

	if artifacts[0].name != "fleeti-installer.iso" {
		t.Fatalf("unexpected artifact name: %s", artifacts[0].name)
	}

	if artifacts[0].path != artifactPath {
		t.Fatalf("unexpected artifact path: %s", artifacts[0].path)
	}
}

func TestCollectInstallerArtifactsResolvesResultSymlinkToFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	artifactPath := filepath.Join(root, "fleeti-installer.iso")
	if err := os.WriteFile(artifactPath, []byte("installer"), 0o644); err != nil {
		t.Fatalf("failed to create installer artifact: %v", err)
	}

	resultPath := filepath.Join(root, "result")
	if err := os.Symlink(artifactPath, resultPath); err != nil {
		t.Fatalf("failed to create result symlink: %v", err)
	}

	artifacts, err := collectInstallerArtifacts(resultPath)
	if err != nil {
		t.Fatalf("collectInstallerArtifacts returned error: %v", err)
	}

	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(artifacts))
	}

	if artifacts[0].name != "fleeti-installer.iso" {
		t.Fatalf("unexpected artifact name: %s", artifacts[0].name)
	}

	if artifacts[0].path != artifactPath {
		t.Fatalf("unexpected artifact path: %s", artifacts[0].path)
	}
}

func TestCollectInstallerArtifactsFailsOnDuplicateNames(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	firstDir := filepath.Join(root, "a")
	secondDir := filepath.Join(root, "b")

	if err := os.MkdirAll(firstDir, 0o755); err != nil {
		t.Fatalf("failed to create first directory: %v", err)
	}

	if err := os.MkdirAll(secondDir, 0o755); err != nil {
		t.Fatalf("failed to create second directory: %v", err)
	}

	firstArtifactPath := filepath.Join(firstDir, "installer.iso")
	secondArtifactPath := filepath.Join(secondDir, "installer.iso")

	if err := os.WriteFile(firstArtifactPath, []byte("first"), 0o644); err != nil {
		t.Fatalf("failed to create first artifact: %v", err)
	}

	if err := os.WriteFile(secondArtifactPath, []byte("second"), 0o644); err != nil {
		t.Fatalf("failed to create second artifact: %v", err)
	}

	_, err := collectInstallerArtifacts(root)
	if err == nil {
		t.Fatal("expected duplicate-name error, got nil")
	}

	if !strings.Contains(err.Error(), "duplicate installer artifact filename installer.iso") {
		t.Fatalf("expected duplicate filename error, got: %v", err)
	}
}

func TestCopyEmbeddedNixOSWorkspaceMakesOverridesWritable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	destinationDir := filepath.Join(root, "nixos")

	if err := copyEmbeddedNixOSWorkspace(destinationDir); err != nil {
		t.Fatalf("copyEmbeddedNixOSWorkspace returned error: %v", err)
	}

	overridesPath := filepath.Join(destinationDir, "modules", "build-overrides.nix")
	if err := os.WriteFile(overridesPath, []byte("{ }\n"), 0o640); err != nil {
		t.Fatalf("expected copied build-overrides.nix to be writable, got: %v", err)
	}
}

func TestWriteBuildOverridesModuleSetsKernelPackages(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	modulesDir := filepath.Join(root, "modules")
	if err := os.MkdirAll(modulesDir, 0o755); err != nil {
		t.Fatalf("failed to create modules directory: %v", err)
	}

	err := writeBuildOverridesModule(root, "1.2.3", "fleet-a", []string{"vim"}, ProfileKernelConfig{
		Attr: "linux_6_19",
	}, ProfileSecurityConfig{}, false, "")
	if err != nil {
		t.Fatalf("writeBuildOverridesModule returned error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(modulesDir, "build-overrides.nix"))
	if err != nil {
		t.Fatalf("failed to read generated build overrides file: %v", err)
	}

	generated := string(content)
	if !strings.Contains(generated, "boot.kernelPackages = lib.mkForce (pkgs.linuxPackagesFor pkgs.linuxKernel.kernels.linux_6_19);") {
		t.Fatalf("expected kernelPackages selection in generated overrides, got: %s", generated)
	}
}

func TestWriteBuildOverridesModuleAddsKernelSourceOverlay(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	modulesDir := filepath.Join(root, "modules")
	if err := os.MkdirAll(modulesDir, 0o755); err != nil {
		t.Fatalf("failed to create modules directory: %v", err)
	}

	err := writeBuildOverridesModule(root, "1.2.3", "fleet-a", []string{}, ProfileKernelConfig{
		Attr: "linux_6_19",
		SourceOverride: ProfileKernelSourceOverride{
			Enabled: true,
			URL:     "https://github.com/example/linux.git",
			Ref:     "refs/tags/v6.19-custom",
			Rev:     "abcd1234abcd1234abcd1234abcd1234abcd1234",
		},
	}, ProfileSecurityConfig{}, false, "")
	if err != nil {
		t.Fatalf("writeBuildOverridesModule returned error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(modulesDir, "build-overrides.nix"))
	if err != nil {
		t.Fatalf("failed to read generated build overrides file: %v", err)
	}

	generated := string(content)
	if !strings.Contains(generated, "sourceOverride = builtins.fetchGit {") {
		t.Fatalf("expected fetchGit source override block, got: %s", generated)
	}

	if !strings.Contains(generated, "url = \"https://github.com/example/linux.git\";") {
		t.Fatalf("expected source override URL in generated overrides, got: %s", generated)
	}

	if !strings.Contains(generated, "rev = \"abcd1234abcd1234abcd1234abcd1234abcd1234\";") {
		t.Fatalf("expected source override revision in generated overrides, got: %s", generated)
	}

	if !strings.Contains(generated, "ref = \"refs/tags/v6.19-custom\";") {
		t.Fatalf("expected source override ref in generated overrides, got: %s", generated)
	}

	if !strings.Contains(generated, "boot.kernelPackages = lib.mkForce pkgs.fleetiKernelPackages;") {
		t.Fatalf("expected kernelPackages override to use overlay output, got: %s", generated)
	}
}

func TestWriteBuildOverridesModuleWritesKernelPatchesInOrder(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	modulesDir := filepath.Join(root, "modules")
	if err := os.MkdirAll(modulesDir, 0o755); err != nil {
		t.Fatalf("failed to create modules directory: %v", err)
	}

	firstPatch := []byte("diff --git a/first b/first\n")
	secondPatch := []byte("diff --git a/second b/second\n")

	firstDigest := sha256.Sum256(firstPatch)
	secondDigest := sha256.Sum256(secondPatch)

	err := writeBuildOverridesModule(root, "1.2.3", "fleet-a", nil, ProfileKernelConfig{
		Attr: "linux_6_19",
		SourceOverride: ProfileKernelSourceOverride{
			Enabled: true,
			URL:     "https://github.com/example/linux.git",
			Rev:     "abcd1234abcd1234abcd1234abcd1234abcd1234",
			Patches: []ProfileKernelPatch{
				{
					Name:          "0002-second.patch",
					SHA256:        hex.EncodeToString(secondDigest[:]),
					ContentBase64: base64.StdEncoding.EncodeToString(secondPatch),
				},
				{
					Name:          "0001-first.patch",
					SHA256:        hex.EncodeToString(firstDigest[:]),
					ContentBase64: base64.StdEncoding.EncodeToString(firstPatch),
				},
			},
		},
	}, ProfileSecurityConfig{}, true, "")
	if err != nil {
		t.Fatalf("writeBuildOverridesModule returned error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(modulesDir, "build-overrides.nix"))
	if err != nil {
		t.Fatalf("failed to read generated build overrides file: %v", err)
	}

	generated := string(content)
	if !strings.Contains(generated, "fleeti.services.openclawMicrovm.enable = lib.mkForce true;") {
		t.Fatalf("expected openclaw microvm override in generated overrides, got: %s", generated)
	}

	if !strings.Contains(generated, "patchedKernelSource = pkgs.runCommand") {
		t.Fatalf("expected patched source runCommand block, got: %s", generated)
	}

	firstCommandIndex := strings.Index(generated, "patch -p1 < ${./kernel-patches/001-0002-second.patch}")
	secondCommandIndex := strings.Index(generated, "patch -p1 < ${./kernel-patches/002-0001-first.patch}")
	if firstCommandIndex < 0 || secondCommandIndex < 0 || firstCommandIndex > secondCommandIndex {
		t.Fatalf("expected patch commands in configured order, got: %s", generated)
	}

	firstPatchPath := filepath.Join(modulesDir, "kernel-patches", "001-0002-second.patch")
	secondPatchPath := filepath.Join(modulesDir, "kernel-patches", "002-0001-first.patch")

	firstBytes, err := os.ReadFile(firstPatchPath)
	if err != nil {
		t.Fatalf("failed to read first patch file: %v", err)
	}

	secondBytes, err := os.ReadFile(secondPatchPath)
	if err != nil {
		t.Fatalf("failed to read second patch file: %v", err)
	}

	if string(firstBytes) != string(secondPatch) {
		t.Fatalf("unexpected first patch file content: %q", string(firstBytes))
	}

	if string(secondBytes) != string(firstPatch) {
		t.Fatalf("unexpected second patch file content: %q", string(secondBytes))
	}
}

func TestWriteBuildOverridesModuleImportsProfileRawNixModule(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	modulesDir := filepath.Join(root, "modules")
	if err := os.MkdirAll(modulesDir, 0o755); err != nil {
		t.Fatalf("failed to create modules directory: %v", err)
	}

	rawNix := `{ config, ... }: { services.openssh.settings.PasswordAuthentication = false; }`
	if err := writeBuildOverridesModule(root, "1.2.3", "fleet-a", []string{"vim"}, ProfileKernelConfig{}, ProfileSecurityConfig{}, false, rawNix); err != nil {
		t.Fatalf("writeBuildOverridesModule returned error: %v", err)
	}

	overridesBytes, err := os.ReadFile(filepath.Join(modulesDir, "build-overrides.nix"))
	if err != nil {
		t.Fatalf("failed to read build overrides file: %v", err)
	}

	if !strings.Contains(string(overridesBytes), "./profile-raw-nix.nix") {
		t.Fatalf("expected build overrides to import profile raw nix module, got: %s", string(overridesBytes))
	}

	rawModuleBytes, err := os.ReadFile(filepath.Join(modulesDir, "profile-raw-nix.nix"))
	if err != nil {
		t.Fatalf("failed to read raw nix module: %v", err)
	}

	if strings.TrimSpace(string(rawModuleBytes)) != rawNix {
		t.Fatalf("expected raw nix module to match input, got: %q", string(rawModuleBytes))
	}
}

func TestWriteBuildOverridesModuleConfiguresSecurityHardening(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	modulesDir := filepath.Join(root, "modules")
	if err := os.MkdirAll(modulesDir, 0o755); err != nil {
		t.Fatalf("failed to create modules directory: %v", err)
	}

	securityConfig := ProfileSecurityConfig{
		Configured: true,
		Firewall: ProfileSecurityFirewallConfig{
			Enable:          true,
			AllowedTCPPorts: []int{443, 22, 80},
			AllowedUDPPorts: []int{53},
		},
		BlacklistedKernelModules: []string{"firewire-core", "usb-storage"},
		AppArmor:                 ProfileSecurityAppArmorConfig{Enable: true},
		PasswordPolicy: ProfileSecurityPasswordPolicyConfig{
			PWQuality: ProfileSecurityPWQualityConfig{
				Enable:        true,
				MinimumLength: 14,
				MinimumDigits: 1,
				MinimumUpper:  1,
				MinimumLower:  1,
				MinimumOther:  1,
				RetryCount:    3,
			},
			Expiry: ProfileSecurityPasswordExpiryConfig{
				Enable:      true,
				MaximumDays: 90,
				WarningDays: 14,
			},
		},
		WebsiteBlocking: ProfileSecurityWebsiteBlockingConfig{
			Enable:          true,
			BlockCategories: []string{"social", "porn"},
		},
	}

	if err := writeBuildOverridesModule(root, "1.2.3", "fleet-a", nil, ProfileKernelConfig{}, securityConfig, false, ""); err != nil {
		t.Fatalf("writeBuildOverridesModule returned error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(modulesDir, "build-overrides.nix"))
	if err != nil {
		t.Fatalf("failed to read generated build overrides file: %v", err)
	}

	generated := string(content)
	if !strings.Contains(generated, "networking.firewall.enable = lib.mkForce true;") {
		t.Fatalf("expected firewall enable override, got: %s", generated)
	}

	if !strings.Contains(generated, "networking.firewall.allowedTCPPorts = lib.mkForce [ 22 80 443 ];") {
		t.Fatalf("expected normalized TCP ports override, got: %s", generated)
	}

	if !strings.Contains(generated, "networking.firewall.allowedUDPPorts = lib.mkForce [ 53 ];") {
		t.Fatalf("expected UDP ports override, got: %s", generated)
	}

	if !strings.Contains(generated, `boot.blacklistedKernelModules = lib.mkForce [ "firewire-core" "usb-storage" ];`) {
		t.Fatalf("expected blacklisted kernel modules override, got: %s", generated)
	}

	if !strings.Contains(generated, "security.apparmor.enable = lib.mkForce true;") {
		t.Fatalf("expected apparmor enable override, got: %s", generated)
	}

	if !strings.Contains(generated, "security.apparmor.packages = lib.mkForce (with pkgs; [ apparmor-utils apparmor-profiles ]);") {
		t.Fatalf("expected apparmor package override, got: %s", generated)
	}

	if !strings.Contains(generated, `security.pam.services.passwd.rules.password.pwquality = {`) {
		t.Fatalf("expected passwd pwquality override, got: %s", generated)
	}

	if !strings.Contains(generated, `modulePath = "${pkgs.libpwquality.lib}/lib/security/pam_pwquality.so";`) {
		t.Fatalf("expected pwquality module path, got: %s", generated)
	}

	if !strings.Contains(generated, "minlen = 14;") || !strings.Contains(generated, "retry = 3;") {
		t.Fatalf("expected pwquality settings, got: %s", generated)
	}

	if !strings.Contains(generated, "security.loginDefs.settings.PASS_MAX_DAYS = lib.mkForce 90;") {
		t.Fatalf("expected password max age override, got: %s", generated)
	}

	if !strings.Contains(generated, "security.loginDefs.settings.PASS_WARN_AGE = lib.mkForce 14;") {
		t.Fatalf("expected password warning age override, got: %s", generated)
	}

	if !strings.Contains(generated, "networking.stevenblack.enable = lib.mkForce true;") {
		t.Fatalf("expected website blocking enable override, got: %s", generated)
	}

	if !strings.Contains(generated, `networking.stevenblack.block = lib.mkForce [ "porn" "social" ];`) {
		t.Fatalf("expected normalized website blocking categories, got: %s", generated)
	}
}
