/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/humaidq/fleeti/v2/db"
)

func TestRecoverQueuedBuildExecutionsRequeuesQueuedWork(t *testing.T) {
	originalListQueuedBuildExecutions := listQueuedBuildExecutions
	originalListQueuedBuildInstallerExecutions := listQueuedBuildInstallerExecutions
	originalRequeueBuildExecution := requeueBuildExecution
	originalRequeueInstallerBuildExecution := requeueInstallerBuildExecution

	t.Cleanup(func() {
		listQueuedBuildExecutions = originalListQueuedBuildExecutions
		listQueuedBuildInstallerExecutions = originalListQueuedBuildInstallerExecutions
		requeueBuildExecution = originalRequeueBuildExecution
		requeueInstallerBuildExecution = originalRequeueInstallerBuildExecution
	})

	listQueuedBuildExecutions = func(context.Context) ([]db.QueuedBuildExecution, error) {
		return []db.QueuedBuildExecution{
			{ID: "build-1", Version: "v1.2.3"},
			{ID: "build-2", Version: "v2.0.0"},
		}, nil
	}

	listQueuedBuildInstallerExecutions = func(context.Context) ([]db.QueuedBuildInstallerExecution, error) {
		return []db.QueuedBuildInstallerExecution{{ID: "build-3"}}, nil
	}

	type queuedBuildCall struct {
		id      string
		version string
	}

	queuedBuildCalls := make([]queuedBuildCall, 0)
	queuedInstallerCalls := make([]string, 0)

	requeueBuildExecution = func(buildID, buildVersion string) {
		queuedBuildCalls = append(queuedBuildCalls, queuedBuildCall{id: buildID, version: buildVersion})
	}

	requeueInstallerBuildExecution = func(buildID string) {
		queuedInstallerCalls = append(queuedInstallerCalls, buildID)
	}

	if err := RecoverQueuedBuildExecutions(context.Background()); err != nil {
		t.Fatalf("RecoverQueuedBuildExecutions returned error: %v", err)
	}

	if got, want := queuedBuildCalls, []queuedBuildCall{{id: "build-1", version: "v1.2.3"}, {id: "build-2", version: "v2.0.0"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected queued build calls: got %#v want %#v", got, want)
	}

	if got, want := queuedInstallerCalls, []string{"build-3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected queued installer build calls: got %#v want %#v", got, want)
	}
}

func TestRecoverQueuedBuildExecutionsReturnsQueuedBuildErrors(t *testing.T) {
	originalListQueuedBuildExecutions := listQueuedBuildExecutions
	originalListQueuedBuildInstallerExecutions := listQueuedBuildInstallerExecutions

	t.Cleanup(func() {
		listQueuedBuildExecutions = originalListQueuedBuildExecutions
		listQueuedBuildInstallerExecutions = originalListQueuedBuildInstallerExecutions
	})

	listQueuedBuildExecutions = func(context.Context) ([]db.QueuedBuildExecution, error) {
		return nil, os.ErrPermission
	}

	listQueuedBuildInstallerExecutions = func(context.Context) ([]db.QueuedBuildInstallerExecution, error) {
		t.Fatal("expected installer recovery list not to be called")
		return nil, nil
	}

	err := RecoverQueuedBuildExecutions(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "failed to list queued builds for recovery") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecoverQueuedBuildExecutionsReturnsQueuedInstallerErrors(t *testing.T) {
	originalListQueuedBuildExecutions := listQueuedBuildExecutions
	originalListQueuedBuildInstallerExecutions := listQueuedBuildInstallerExecutions

	t.Cleanup(func() {
		listQueuedBuildExecutions = originalListQueuedBuildExecutions
		listQueuedBuildInstallerExecutions = originalListQueuedBuildInstallerExecutions
	})

	listQueuedBuildExecutions = func(context.Context) ([]db.QueuedBuildExecution, error) {
		return nil, nil
	}

	listQueuedBuildInstallerExecutions = func(context.Context) ([]db.QueuedBuildInstallerExecution, error) {
		return nil, os.ErrPermission
	}

	err := RecoverQueuedBuildExecutions(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "failed to list queued installer builds for recovery") {
		t.Fatalf("unexpected error: %v", err)
	}
}

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

func TestPublishBuildArtifactsPreservesChecksumManifest(t *testing.T) {
	t.Parallel()

	resultDir := t.TempDir()
	updatesDir := t.TempDir()

	rawArtifactPath := filepath.Join(resultDir, "fleeti_v1.2.3.nix-store.raw.xz")
	if err := os.WriteFile(rawArtifactPath, []byte("raw"), 0o444); err != nil {
		t.Fatalf("failed to create raw artifact: %v", err)
	}

	ukiArtifactPath := filepath.Join(resultDir, "fleeti_v1.2.3.efi.xz")
	if err := os.WriteFile(ukiArtifactPath, []byte("uki"), 0o444); err != nil {
		t.Fatalf("failed to create uki artifact: %v", err)
	}

	checksumPath := filepath.Join(resultDir, checksumManifestFileName)
	if err := os.WriteFile(checksumPath, []byte("checksum manifest\n"), 0o444); err != nil {
		t.Fatalf("failed to create checksum manifest: %v", err)
	}

	artifactURL, err := publishBuildArtifacts(resultDir, updatesDir, "build-1")
	if err != nil {
		t.Fatalf("publishBuildArtifacts returned error: %v", err)
	}

	if artifactURL != "/update/artifacts/build-1/fleeti_v1.2.3.nix-store.raw.xz" {
		t.Fatalf("unexpected primary artifact URL: %s", artifactURL)
	}

	publishedChecksumPath := filepath.Join(updatesDir, updatesArtifactsDirName, "build-1", checksumManifestFileName)
	publishedChecksum, err := os.ReadFile(publishedChecksumPath)
	if err != nil {
		t.Fatalf("failed to read published checksum manifest: %v", err)
	}

	if string(publishedChecksum) != "checksum manifest\n" {
		t.Fatalf("unexpected published checksum manifest contents: %q", publishedChecksum)
	}
}

func TestActivateFleetReleaseArtifactsPreservesChecksumManifest(t *testing.T) {
	t.Parallel()

	resultDir := t.TempDir()
	updatesDir := t.TempDir()
	fleetID := "11111111-1111-1111-1111-111111111111"

	if err := os.WriteFile(filepath.Join(resultDir, "fleeti_v1.2.3.nix-store.raw.xz"), []byte("raw"), 0o444); err != nil {
		t.Fatalf("failed to create raw artifact: %v", err)
	}

	if err := os.WriteFile(filepath.Join(resultDir, "fleeti_v1.2.3.efi.xz"), []byte("uki"), 0o444); err != nil {
		t.Fatalf("failed to create uki artifact: %v", err)
	}

	if err := os.WriteFile(filepath.Join(resultDir, checksumManifestFileName), []byte("checksum manifest\n"), 0o444); err != nil {
		t.Fatalf("failed to create checksum manifest: %v", err)
	}

	if _, err := publishBuildArtifacts(resultDir, updatesDir, "build-1"); err != nil {
		t.Fatalf("publishBuildArtifacts returned error: %v", err)
	}

	if err := activateFleetReleaseArtifacts(updatesDir, fleetID, "build-1", "v1.2.3"); err != nil {
		t.Fatalf("activateFleetReleaseArtifacts returned error: %v", err)
	}

	activatedChecksumPath := filepath.Join(updatesDir, fleetID, checksumManifestFileName)
	activatedChecksum, err := os.ReadFile(activatedChecksumPath)
	if err != nil {
		t.Fatalf("failed to read activated checksum manifest: %v", err)
	}

	if string(activatedChecksum) != "checksum manifest\n" {
		t.Fatalf("unexpected activated checksum manifest contents: %q", activatedChecksum)
	}
}

func TestActivateFleetReleaseArtifactsRenamesToReleaseVersion(t *testing.T) {
	t.Parallel()

	resultDir := t.TempDir()
	updatesDir := t.TempDir()
	fleetID := "11111111-1111-1111-1111-111111111111"

	if err := os.WriteFile(filepath.Join(resultDir, "fleeti_v1.2.3.nix-store.raw.xz"), []byte("raw"), 0o444); err != nil {
		t.Fatalf("failed to create raw artifact: %v", err)
	}

	if err := os.WriteFile(filepath.Join(resultDir, "fleeti_v1.2.3.efi.xz"), []byte("uki"), 0o444); err != nil {
		t.Fatalf("failed to create uki artifact: %v", err)
	}

	if _, err := publishBuildArtifacts(resultDir, updatesDir, "build-1"); err != nil {
		t.Fatalf("publishBuildArtifacts returned error: %v", err)
	}

	// Build was v1.2.3 but the release uses a different version; activated
	// artifacts must carry the release version so sysupdate sees it.
	if err := activateFleetReleaseArtifacts(updatesDir, fleetID, "build-1", "v2.0.0"); err != nil {
		t.Fatalf("activateFleetReleaseArtifacts returned error: %v", err)
	}

	fleetDir := filepath.Join(updatesDir, fleetID)

	for _, expected := range []string{"fleeti_v2.0.0.nix-store.raw.xz", "fleeti_v2.0.0.efi.xz"} {
		if _, err := os.Stat(filepath.Join(fleetDir, expected)); err != nil {
			t.Fatalf("expected activated artifact %s: %v", expected, err)
		}
	}

	for _, unexpected := range []string{"fleeti_v1.2.3.nix-store.raw.xz", "fleeti_v1.2.3.efi.xz"} {
		if _, err := os.Stat(filepath.Join(fleetDir, unexpected)); !os.IsNotExist(err) {
			t.Fatalf("expected build-version artifact %s to be absent, stat err: %v", unexpected, err)
		}
	}
}

func TestRenameArtifactToReleaseVersion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		artifact       string
		releaseVersion string
		want           string
	}{
		{"nix-store raw xz", "fleeti_v1.2.3.nix-store.raw.xz", "v2.0.0", "fleeti_v2.0.0.nix-store.raw.xz"},
		{"nix-store raw", "fleeti_v1.2.3.nix-store.raw", "v2.0.0", "fleeti_v2.0.0.nix-store.raw"},
		{"uki efi xz", "fleeti_v1.2.3.efi.xz", "v2.0.0", "fleeti_v2.0.0.efi.xz"},
		{"uki efi", "fleeti_v1.2.3.efi", "v2.0.0", "fleeti_v2.0.0.efi"},
		{"checksum manifest passthrough", "SHA256SUMS", "v2.0.0", "SHA256SUMS"},
		{"unknown suffix passthrough", "notes.txt", "v2.0.0", "notes.txt"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := renameArtifactToReleaseVersion(tc.artifact, tc.releaseVersion); got != tc.want {
				t.Fatalf("renameArtifactToReleaseVersion(%q, %q) = %q, want %q", tc.artifact, tc.releaseVersion, got, tc.want)
			}
		})
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

func TestWriteBuildOverridesModuleUsesDefaultFleetiInstanceURL(t *testing.T) {
	root := t.TempDir()
	modulesDir := filepath.Join(root, "modules")
	if err := os.MkdirAll(modulesDir, 0o755); err != nil {
		t.Fatalf("failed to create modules directory: %v", err)
	}

	if err := writeBuildOverridesModule(root, "1.2.3", "fleet-a", nil, ProfileKernelConfig{}, ProfileSecurityConfig{}, false, ""); err != nil {
		t.Fatalf("writeBuildOverridesModule returned error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(modulesDir, "build-overrides.nix"))
	if err != nil {
		t.Fatalf("failed to read generated build overrides file: %v", err)
	}

	generated := string(content)
	if !strings.Contains(generated, `systemd.sysupdate.transfers."10-nix-store".Source.Path = lib.mkForce "https://admin.fleeti.ae/update/fleet-a/";`) {
		t.Fatalf("expected default Fleeti instance URL in generated overrides, got: %s", generated)
	}

	if !strings.Contains(generated, `systemd.sysupdate.transfers."20-boot-image".Source.Path = lib.mkForce "https://admin.fleeti.ae/update/fleet-a/";`) {
		t.Fatalf("expected default Fleeti instance URL for boot image in generated overrides, got: %s", generated)
	}

	if !strings.Contains(generated, `fleeti.services.admind.fleetId = "fleet-a";`) {
		t.Fatalf("expected admind fleetId in generated overrides, got: %s", generated)
	}

	if !strings.Contains(generated, `fleeti.services.admind.serverUrl = "https://admin.fleeti.ae";`) {
		t.Fatalf("expected admind serverUrl in generated overrides, got: %s", generated)
	}
}

func TestWriteBuildOverridesModuleUsesConfiguredFleetiInstanceURLPrefix(t *testing.T) {
	t.Setenv(fleetiInstanceBaseURLEnvVar, "https://192.168.1.123:8080/fleeti/")

	root := t.TempDir()
	modulesDir := filepath.Join(root, "modules")
	if err := os.MkdirAll(modulesDir, 0o755); err != nil {
		t.Fatalf("failed to create modules directory: %v", err)
	}

	if err := writeBuildOverridesModule(root, "1.2.3", "fleet-a", nil, ProfileKernelConfig{}, ProfileSecurityConfig{}, false, ""); err != nil {
		t.Fatalf("writeBuildOverridesModule returned error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(modulesDir, "build-overrides.nix"))
	if err != nil {
		t.Fatalf("failed to read generated build overrides file: %v", err)
	}

	generated := string(content)
	expected := `systemd.sysupdate.transfers."10-nix-store".Source.Path = lib.mkForce "https://192.168.1.123:8080/fleeti/update/fleet-a/";`
	if !strings.Contains(generated, expected) {
		t.Fatalf("expected configured Fleeti instance URL with prefix in generated overrides, got: %s", generated)
	}

	if !strings.Contains(generated, `fleeti.services.admind.serverUrl = "https://192.168.1.123:8080/fleeti";`) {
		t.Fatalf("expected admind serverUrl with prefix in generated overrides, got: %s", generated)
	}
}

func TestWriteBuildOverridesModuleAllowsHTTPFleetiInstanceURL(t *testing.T) {
	t.Setenv(fleetiInstanceBaseURLEnvVar, "http://192.168.1.123:8080")

	root := t.TempDir()
	modulesDir := filepath.Join(root, "modules")
	if err := os.MkdirAll(modulesDir, 0o755); err != nil {
		t.Fatalf("failed to create modules directory: %v", err)
	}

	if err := writeBuildOverridesModule(root, "1.2.3", "fleet-a", nil, ProfileKernelConfig{}, ProfileSecurityConfig{}, false, ""); err != nil {
		t.Fatalf("writeBuildOverridesModule returned error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(modulesDir, "build-overrides.nix"))
	if err != nil {
		t.Fatalf("failed to read generated build overrides file: %v", err)
	}

	generated := string(content)
	if !strings.Contains(generated, `systemd.sysupdate.transfers."10-nix-store".Source.Path = lib.mkForce "http://192.168.1.123:8080/update/fleet-a/";`) {
		t.Fatalf("expected HTTP Fleeti instance URL in generated overrides, got: %s", generated)
	}

	if !strings.Contains(generated, `fleeti.services.admind.serverUrl = "http://192.168.1.123:8080";`) {
		t.Fatalf("expected admind serverUrl over HTTP in generated overrides, got: %s", generated)
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
