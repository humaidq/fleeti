/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
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
	})
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
	})
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
