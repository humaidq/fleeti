/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/humaidq/fleeti/v2/db"
)

const (
	nixosSourceDirName      = "nixos"
	updatesDirName          = "updates"
	updatesArtifactsDirName = "artifacts"
	installerArtifactsDir   = "installer"
	buildOverridesPath      = "modules/build-overrides.nix"
	buildLogFlushSize       = 4096
	updateBuildTarget       = ".#fleeti-update"
	installerBuildTarget    = ".#fleeti-installer"
)

var safeUpdatePathSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

func queueBuildExecution(buildID, buildVersion string) {
	go executeBuild(buildID, buildVersion)
}

func queueInstallerBuildExecution(buildID string) {
	go executeInstallerBuild(buildID)
}

func executeBuild(buildID, buildVersion string) {
	ctx := context.Background()

	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}

		logger.Error("build execution panicked", "build_id", buildID, "panic", recovered)

		if err := db.UpdateBuild(ctx, buildID, db.BuildStatusFailed, ""); err != nil {
			logger.Error("failed to mark panicked build as failed", "build_id", buildID, "error", err)
		}
	}()

	if err := db.UpdateBuild(ctx, buildID, db.BuildStatusRunning, ""); err != nil {
		logger.Error("failed to mark build as running", "build_id", buildID, "error", err)

		return
	}

	artifactURL, err := runBuildAndPublishUpdate(ctx, buildID, buildVersion)
	if err != nil {
		logger.Error("build execution failed", "build_id", buildID, "error", err)

		if updateErr := db.UpdateBuild(ctx, buildID, db.BuildStatusFailed, ""); updateErr != nil {
			logger.Error("failed to mark build as failed", "build_id", buildID, "error", updateErr)
		}

		return
	}

	if err := db.UpdateBuild(ctx, buildID, db.BuildStatusSucceeded, artifactURL); err != nil {
		logger.Error("failed to mark build as succeeded", "build_id", buildID, "artifact", artifactURL, "error", err)

		return
	}

	logger.Info("build execution completed", "build_id", buildID, "artifact", artifactURL)
}

func executeInstallerBuild(buildID string) {
	ctx := context.Background()

	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}

		logger.Error("installer build execution panicked", "build_id", buildID, "panic", recovered)

		if err := db.UpdateBuildInstaller(ctx, buildID, db.BuildInstallerStatusFailed, ""); err != nil {
			logger.Error("failed to mark panicked installer build as failed", "build_id", buildID, "error", err)
		}
	}()

	if err := db.UpdateBuildInstaller(ctx, buildID, db.BuildInstallerStatusRunning, ""); err != nil {
		logger.Error("failed to mark installer build as running", "build_id", buildID, "error", err)

		return
	}

	artifactURL, err := runBuildAndPublishInstaller(ctx, buildID)
	if err != nil {
		logger.Error("installer build execution failed", "build_id", buildID, "error", err)

		if updateErr := db.UpdateBuildInstaller(ctx, buildID, db.BuildInstallerStatusFailed, ""); updateErr != nil {
			logger.Error("failed to mark installer build as failed", "build_id", buildID, "error", updateErr)
		}

		return
	}

	if err := db.UpdateBuildInstaller(ctx, buildID, db.BuildInstallerStatusSucceeded, artifactURL); err != nil {
		logger.Error("failed to mark installer build as succeeded", "build_id", buildID, "artifact", artifactURL, "error", err)

		return
	}

	logger.Info("installer build execution completed", "build_id", buildID, "artifact", artifactURL)
}

func runBuildAndPublishUpdate(ctx context.Context, buildID, buildVersion string) (string, error) {
	buildVersion = strings.TrimSpace(buildVersion)
	if buildVersion == "" {
		return "", fmt.Errorf("build version is required")
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to resolve working directory: %w", err)
	}

	nixosSourceDir := filepath.Join(workingDir, nixosSourceDirName)
	nixosInfo, err := os.Stat(nixosSourceDir)
	if err != nil {
		return "", fmt.Errorf("failed to access nixos source directory: %w", err)
	}

	if !nixosInfo.IsDir() {
		return "", fmt.Errorf("nixos source is not a directory: %s", nixosSourceDir)
	}

	updatesDir, err := resolveUpdatesDirectory()
	if err != nil {
		return "", err
	}

	workspaceRoot, err := os.MkdirTemp("", "fleeti-build-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary build workspace: %w", err)
	}

	defer func() {
		if removeErr := os.RemoveAll(workspaceRoot); removeErr != nil {
			logger.Warn("failed to clean temporary build workspace", "workspace", workspaceRoot, "error", removeErr)
		}
	}()

	workspaceNixOSDir := filepath.Join(workspaceRoot, nixosSourceDirName)
	if err := copyDirectoryWithFilters(nixosSourceDir, workspaceNixOSDir); err != nil {
		return "", fmt.Errorf("failed to copy nixos workspace: %w", err)
	}

	profileConfigJSON, fleetID, err := db.GetBuildExecutionMetadata(ctx, buildID)
	if err != nil {
		return "", fmt.Errorf("failed to load build profile configuration: %w", err)
	}

	packages, err := packagesFromProfileConfig(profileConfigJSON)
	if err != nil {
		return "", fmt.Errorf("failed to parse profile packages: %w", err)
	}

	kernelConfig, err := profileKernelConfigFromProfileConfig(profileConfigJSON)
	if err != nil {
		return "", fmt.Errorf("failed to parse profile kernel config: %w", err)
	}

	if err := validateProfileKernelConfig(kernelConfig, nil); err != nil {
		return "", fmt.Errorf("invalid profile kernel config: %w", err)
	}

	if err := writeBuildOverridesModule(workspaceNixOSDir, buildVersion, fleetID, packages, kernelConfig); err != nil {
		return "", err
	}

	if err := runNixBuildCommand(ctx, buildID, workspaceNixOSDir, updateBuildTarget, false); err != nil {
		return "", err
	}

	resultDir := filepath.Join(workspaceNixOSDir, "result")
	artifactURL, err := publishBuildArtifacts(resultDir, updatesDir, buildID)
	if err != nil {
		return "", fmt.Errorf("failed to publish build artifacts: %w", err)
	}

	return artifactURL, nil
}

func runBuildAndPublishInstaller(ctx context.Context, buildID string) (string, error) {
	buildID = strings.TrimSpace(buildID)
	if buildID == "" {
		return "", fmt.Errorf("build ID is required")
	}

	build, err := db.GetBuildByID(ctx, buildID)
	if err != nil {
		return "", fmt.Errorf("failed to load build for installer generation: %w", err)
	}

	if build.Status != db.BuildStatusSucceeded {
		return "", db.ErrBuildNotReadyForInstaller
	}

	profileConfigJSON, fleetID, err := db.GetBuildExecutionMetadata(ctx, buildID)
	if err != nil {
		return "", fmt.Errorf("failed to load build profile configuration: %w", err)
	}

	packages, err := packagesFromProfileConfig(profileConfigJSON)
	if err != nil {
		return "", fmt.Errorf("failed to parse profile packages: %w", err)
	}

	kernelConfig, err := profileKernelConfigFromProfileConfig(profileConfigJSON)
	if err != nil {
		return "", fmt.Errorf("failed to parse profile kernel config: %w", err)
	}

	if err := validateProfileKernelConfig(kernelConfig, nil); err != nil {
		return "", fmt.Errorf("invalid profile kernel config: %w", err)
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to resolve working directory: %w", err)
	}

	nixosSourceDir := filepath.Join(workingDir, nixosSourceDirName)
	nixosInfo, err := os.Stat(nixosSourceDir)
	if err != nil {
		return "", fmt.Errorf("failed to access nixos source directory: %w", err)
	}

	if !nixosInfo.IsDir() {
		return "", fmt.Errorf("nixos source is not a directory: %s", nixosSourceDir)
	}

	updatesDir, err := resolveUpdatesDirectory()
	if err != nil {
		return "", err
	}

	workspaceRoot, err := os.MkdirTemp("", "fleeti-installer-build-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary installer build workspace: %w", err)
	}

	defer func() {
		if removeErr := os.RemoveAll(workspaceRoot); removeErr != nil {
			logger.Warn("failed to clean temporary installer build workspace", "workspace", workspaceRoot, "error", removeErr)
		}
	}()

	workspaceNixOSDir := filepath.Join(workspaceRoot, nixosSourceDirName)
	if err := copyDirectoryWithFilters(nixosSourceDir, workspaceNixOSDir); err != nil {
		return "", fmt.Errorf("failed to copy nixos workspace: %w", err)
	}

	if err := writeBuildOverridesModule(workspaceNixOSDir, build.Version, fleetID, packages, kernelConfig); err != nil {
		return "", err
	}

	if err := runNixBuildCommand(ctx, buildID, workspaceNixOSDir, installerBuildTarget, true); err != nil {
		return "", err
	}

	resultPath := filepath.Join(workspaceNixOSDir, "result")
	artifactURL, err := publishBuildInstallerArtifact(resultPath, updatesDir, buildID)
	if err != nil {
		return "", fmt.Errorf("failed to publish installer artifact: %w", err)
	}

	return artifactURL, nil
}

func copyDirectoryWithFilters(sourceDir, destinationDir string) error {
	if err := os.MkdirAll(destinationDir, 0o750); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	return filepath.WalkDir(sourceDir, func(currentPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		relPath, err := filepath.Rel(sourceDir, currentPath)
		if err != nil {
			return fmt.Errorf("failed to resolve relative path: %w", err)
		}

		if relPath == "." {
			return nil
		}

		if shouldSkipWorkspaceEntry(relPath, entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}

			return nil
		}

		targetPath := filepath.Join(destinationDir, relPath)
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("failed to read file info for %s: %w", relPath, err)
		}

		if entry.IsDir() {
			if err := os.MkdirAll(targetPath, info.Mode().Perm()); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", relPath, err)
			}

			return nil
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0o750); err != nil {
			return fmt.Errorf("failed to create parent directory for %s: %w", relPath, err)
		}

		if entry.Type()&os.ModeSymlink != 0 {
			symlinkTarget, err := os.Readlink(currentPath)
			if err != nil {
				return fmt.Errorf("failed to read symlink for %s: %w", relPath, err)
			}

			if err := os.Symlink(symlinkTarget, targetPath); err != nil {
				return fmt.Errorf("failed to copy symlink for %s: %w", relPath, err)
			}

			return nil
		}

		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type in workspace copy: %s", relPath)
		}

		if err := copyFile(currentPath, targetPath, info.Mode().Perm()); err != nil {
			return fmt.Errorf("failed to copy file %s: %w", relPath, err)
		}

		return nil
	})
}

func shouldSkipWorkspaceEntry(relPath string, entry fs.DirEntry) bool {
	base := filepath.Base(relPath)

	if base == "result" || base == "demo-disk.raw" {
		return true
	}

	if entry.IsDir() && base == ".git" {
		return true
	}

	return false
}

func runNixBuildCommand(ctx context.Context, buildID, workspaceNixOSDir, buildTarget string, installerLogs bool) error {
	buildTarget = strings.TrimSpace(buildTarget)
	if buildTarget == "" {
		return fmt.Errorf("build target is required")
	}

	cmd := exec.Command("nix", "build", "--option", "builders", "", buildTarget)
	cmd.Dir = workspaceNixOSDir

	var output bytes.Buffer
	logWriter := newPersistentBuildLogWriter(ctx, buildID, installerLogs)
	defer logWriter.Flush()

	multiWriter := io.MultiWriter(&output, logWriter)
	cmd.Stdout = multiWriter
	cmd.Stderr = multiWriter

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("nix build failed: %w: %s", err, trimBuildOutput(output.String(), 8192))
	}

	return nil
}

type persistentBuildLogWriter struct {
	ctx              context.Context
	buildID          string
	appendChunk      func(context.Context, string, string) error
	mu               sync.Mutex
	buffer           bytes.Buffer
	persistErrorSeen bool
}

func newPersistentBuildLogWriter(ctx context.Context, buildID string, installerLogs bool) *persistentBuildLogWriter {
	appendChunk := db.AppendBuildLogChunk
	if installerLogs {
		appendChunk = db.AppendBuildInstallerLogChunk
	}

	return &persistentBuildLogWriter{
		ctx:         ctx,
		buildID:     strings.TrimSpace(buildID),
		appendChunk: appendChunk,
	}
}

func (w *persistentBuildLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	written, _ := w.buffer.Write(p)
	if w.buffer.Len() >= buildLogFlushSize || bytes.IndexByte(p, '\n') >= 0 {
		w.flushLocked()
	}

	return written, nil
}

func (w *persistentBuildLogWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.flushLocked()
}

func (w *persistentBuildLogWriter) flushLocked() {
	if w.buffer.Len() == 0 {
		return
	}

	chunk := string(append([]byte(nil), w.buffer.Bytes()...))
	w.buffer.Reset()

	if err := w.appendChunk(w.ctx, w.buildID, chunk); err != nil {
		if !w.persistErrorSeen {
			logger.Error("failed to persist build log", "build_id", w.buildID, "error", err)
			w.persistErrorSeen = true
		}
	}
}

func writeBuildOverridesModule(workspaceNixOSDir, buildVersion, fleetID string, packages []string, kernelConfig ProfileKernelConfig) error {
	packageExpressions, err := buildPackageExpressions(packages)
	if err != nil {
		return fmt.Errorf("failed to build package expressions: %w", err)
	}

	kernelOverridesBlock, err := buildKernelOverridesBlock(kernelConfig)
	if err != nil {
		return fmt.Errorf("failed to build kernel overrides: %w", err)
	}

	fleetID = strings.TrimSpace(fleetID)
	if fleetID == "" {
		return fmt.Errorf("fleet ID is required for build override generation")
	}

	updateBasePath := "/update/" + fleetID + "/"
	updateSourcePath := "http://10.10.0.14:8080" + updateBasePath

	packageLines := ""
	if len(packageExpressions) > 0 {
		packageLines = "    " + strings.Join(packageExpressions, "\n    ") + "\n"
	}

	overridesModulePath := filepath.Join(workspaceNixOSDir, buildOverridesPath)
	overridesModuleBody := fmt.Sprintf(`{ lib, pkgs, ... }:
{
	system.image.version = "%s";

	systemd.sysupdate.transfers."10-nix-store".Source.Path = lib.mkForce "%s";
	systemd.sysupdate.transfers."20-boot-image".Source.Path = lib.mkForce "%s";

	%s

	environment.systemPackages = with pkgs; [
%s  ];
}
`, escapeNixString(buildVersion), escapeNixString(updateSourcePath), escapeNixString(updateSourcePath), kernelOverridesBlock, packageLines)

	if err := os.WriteFile(overridesModulePath, []byte(overridesModuleBody), 0o640); err != nil {
		return fmt.Errorf("failed to write build overrides module: %w", err)
	}

	return nil
}

func buildKernelOverridesBlock(kernelConfig ProfileKernelConfig) (string, error) {
	normalized := normalizeProfileKernelConfig(kernelConfig)
	if normalized.Attr == "" {
		return "", nil
	}

	if err := validateProfileKernelConfig(normalized, nil); err != nil {
		return "", err
	}

	kernelExpression := "pkgs.linuxKernel.kernels." + normalized.Attr
	if !normalized.SourceOverride.Enabled {
		return fmt.Sprintf("boot.kernelPackages = lib.mkForce (pkgs.linuxPackagesFor %s);", kernelExpression), nil
	}

	refLine := ""
	if normalized.SourceOverride.Ref != "" {
		refLine = "          ref = \"" + escapeNixString(normalized.SourceOverride.Ref) + "\";\n"
	}

	return fmt.Sprintf(`nixpkgs.overlays = [
	(final: prev: {
		fleetiKernel = let
			baseKernel = prev.linuxKernel.kernels.%s;
			sourceOverride = builtins.fetchGit {
				url = "%s";
				rev = "%s";
%s			};
		in
		baseKernel.override {
			argsOverride = {
				src = sourceOverride;
				version = baseKernel.version;
				modDirVersion = baseKernel.version;
			};
		};

		fleetiKernelPackages = final.linuxPackagesFor final.fleetiKernel;
	})
];

boot.kernelPackages = lib.mkForce pkgs.fleetiKernelPackages;`,
		normalized.Attr,
		escapeNixString(normalized.SourceOverride.URL),
		escapeNixString(normalized.SourceOverride.Rev),
		refLine,
	), nil
}

var (
	nixPackageIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_']*$`)
	nixPackageSegmentPattern    = regexp.MustCompile(`^[A-Za-z0-9_+'-]+$`)
)

func buildPackageExpressions(packages []string) ([]string, error) {
	normalized := normalizePackageList(packages)
	if len(normalized) == 0 {
		return []string{}, nil
	}

	expressions := make([]string, 0, len(normalized))
	for _, pkg := range normalized {
		expression, err := packageNameToNixExpression(pkg)
		if err != nil {
			return nil, fmt.Errorf("package %q: %w", pkg, err)
		}

		expressions = append(expressions, expression)
	}

	return expressions, nil
}

func packageNameToNixExpression(packageName string) (string, error) {
	trimmed := strings.TrimSpace(packageName)
	if trimmed == "" {
		return "", fmt.Errorf("package name is empty")
	}

	segments := strings.Split(trimmed, ".")
	for _, segment := range segments {
		if segment == "" {
			return "", fmt.Errorf("package name contains empty segment")
		}

		if !nixPackageSegmentPattern.MatchString(segment) {
			return "", fmt.Errorf("package name contains unsupported characters")
		}
	}

	parts := make([]string, 0, len(segments))
	requiresPKGSPrefix := false

	for _, segment := range segments {
		if nixPackageIdentifierPattern.MatchString(segment) {
			parts = append(parts, segment)

			continue
		}

		requiresPKGSPrefix = true
		parts = append(parts, `"`+escapeNixString(segment)+`"`)
	}

	expression := strings.Join(parts, ".")
	if requiresPKGSPrefix {
		expression = "pkgs." + expression
	}

	return expression, nil
}

var nixStringEscaper = strings.NewReplacer(
	"\\", "\\\\",
	"\"", "\\\"",
	"$", "\\$",
	"\n", "\\n",
	"\r", "\\r",
	"\t", "\\t",
)

func escapeNixString(value string) string {
	return nixStringEscaper.Replace(value)
}

func publishBuildArtifacts(resultDir, updatesDir, buildID string) (string, error) {
	buildID = strings.TrimSpace(buildID)
	if buildID == "" {
		return "", fmt.Errorf("build ID is required")
	}

	publishedBuildDir := filepath.Join(updatesDir, updatesArtifactsDirName, buildID)
	if err := os.MkdirAll(publishedBuildDir, 0o750); err != nil {
		return "", fmt.Errorf("failed to create build artifacts directory: %w", err)
	}

	entries, err := os.ReadDir(resultDir)
	if err != nil {
		return "", fmt.Errorf("failed to read build result directory: %w", err)
	}

	if len(entries) == 0 {
		return "", fmt.Errorf("build produced no artifacts")
	}

	artifactNames := make([]string, 0, len(entries))

	for _, entry := range entries {
		name := entry.Name()

		if name == checksumManifestFileName {
			logger.Warn("ignoring pre-generated checksum manifest", "file", name)

			continue
		}

		if !isUpdateArtifactFileName(name) {
			logger.Warn("ignoring non-matching build artifact", "file", name)

			continue
		}

		if entry.IsDir() {
			logger.Warn("ignoring artifact directory", "file", name)

			continue
		}

		sourcePath := filepath.Join(resultDir, name)
		destinationPath := filepath.Join(publishedBuildDir, name)

		info, err := entry.Info()
		if err != nil {
			return "", fmt.Errorf("failed to read artifact metadata for %s: %w", name, err)
		}

		if !info.Mode().IsRegular() {
			logger.Warn("ignoring non-regular artifact", "file", name)

			continue
		}

		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}

		if err := copyFile(sourcePath, destinationPath, mode); err != nil {
			return "", fmt.Errorf("failed to copy artifact %s: %w", name, err)
		}

		artifactNames = append(artifactNames, name)
	}

	if len(artifactNames) == 0 {
		return "", fmt.Errorf("build produced no artifacts matching update naming patterns")
	}

	sort.Strings(artifactNames)

	return choosePrimaryArtifactURL(buildID, artifactNames), nil
}

func publishBuildInstallerArtifact(resultPath, updatesDir, buildID string) (string, error) {
	buildID = strings.TrimSpace(buildID)
	if buildID == "" {
		return "", fmt.Errorf("build ID is required")
	}

	publishedInstallerDir := filepath.Join(updatesDir, updatesArtifactsDirName, buildID, installerArtifactsDir)
	if err := os.MkdirAll(publishedInstallerDir, 0o750); err != nil {
		return "", fmt.Errorf("failed to create installer artifacts directory: %w", err)
	}

	artifacts, err := collectInstallerArtifacts(resultPath)
	if err != nil {
		return "", err
	}

	artifactNames := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		mode := artifact.mode
		if mode == 0 {
			mode = 0o644
		}

		destinationPath := filepath.Join(publishedInstallerDir, artifact.name)
		if err := copyFile(artifact.path, destinationPath, mode); err != nil {
			return "", fmt.Errorf("failed to copy installer artifact %s: %w", artifact.name, err)
		}

		artifactNames = append(artifactNames, artifact.name)
	}

	if len(artifactNames) == 0 {
		return "", fmt.Errorf("installer build produced no artifacts")
	}

	sort.Strings(artifactNames)

	return choosePrimaryInstallerArtifactURL(buildID, artifactNames), nil
}

func choosePrimaryArtifactURL(buildID string, artifactNames []string) string {
	prefix := "/update/" + url.PathEscape(updatesArtifactsDirName) + "/" + url.PathEscape(buildID) + "/"

	for _, name := range artifactNames {
		if strings.HasSuffix(name, ".nix-store.raw") {
			return prefix + url.PathEscape(name)
		}
	}

	for _, name := range artifactNames {
		if strings.HasSuffix(name, ".efi") {
			return prefix + url.PathEscape(name)
		}
	}

	for _, name := range artifactNames {
		if name == checksumManifestFileName {
			return prefix + url.PathEscape(name)
		}
	}

	return prefix + url.PathEscape(artifactNames[0])
}

func choosePrimaryInstallerArtifactURL(buildID string, artifactNames []string) string {
	prefix := "/update/" + url.PathEscape(updatesArtifactsDirName) + "/" + url.PathEscape(buildID) + "/" + url.PathEscape(installerArtifactsDir) + "/"

	for _, name := range artifactNames {
		if strings.HasSuffix(strings.ToLower(name), ".iso") {
			return prefix + url.PathEscape(name)
		}
	}

	return prefix + url.PathEscape(artifactNames[0])
}

func collectInstallerArtifacts(resultPath string) ([]publishedArtifact, error) {
	if resolvedPath, err := filepath.EvalSymlinks(resultPath); err == nil {
		resultPath = resolvedPath
	}

	info, err := os.Stat(resultPath)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect installer build result: %w", err)
	}

	artifacts := make([]publishedArtifact, 0)

	if info.Mode().IsRegular() {
		name := filepath.Base(resultPath)
		if !isInstallerArtifactFileName(name) {
			return nil, fmt.Errorf("installer build result is not an ISO artifact")
		}

		return []publishedArtifact{
			{
				name: name,
				path: resultPath,
				mode: info.Mode().Perm(),
			},
		}, nil
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("installer build result is neither a file nor directory")
	}

	seenArtifactNames := make(map[string]string)
	err = filepath.WalkDir(resultPath, func(currentPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() {
			return nil
		}

		name := entry.Name()
		if !isInstallerArtifactFileName(name) {
			return nil
		}

		entryInfo, err := os.Stat(currentPath)
		if err != nil {
			return fmt.Errorf("failed to read installer artifact metadata for %s: %w", name, err)
		}

		if !entryInfo.Mode().IsRegular() {
			return nil
		}

		if existingPath, exists := seenArtifactNames[name]; exists {
			return fmt.Errorf("duplicate installer artifact filename %s found at %s and %s", name, existingPath, currentPath)
		}

		seenArtifactNames[name] = currentPath
		artifacts = append(artifacts, publishedArtifact{
			name: name,
			path: currentPath,
			mode: entryInfo.Mode().Perm(),
		})

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to collect installer artifacts: %w", err)
	}

	if len(artifacts) == 0 {
		return nil, fmt.Errorf("installer build produced no ISO artifacts")
	}

	sort.Slice(artifacts, func(i, j int) bool {
		return artifacts[i].name < artifacts[j].name
	})

	return artifacts, nil
}

func isInstallerArtifactFileName(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		return false
	}

	if strings.Contains(normalized, "/") {
		return false
	}

	return strings.HasSuffix(normalized, ".iso") || strings.HasSuffix(normalized, ".iso.zst")
}

func resolveUpdatesDirectory() (string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to resolve working directory: %w", err)
	}

	updatesDir := filepath.Join(workingDir, updatesDirName)
	if err := os.MkdirAll(updatesDir, 0o750); err != nil {
		return "", fmt.Errorf("failed to create updates directory: %w", err)
	}

	return updatesDir, nil
}

type publishedArtifact struct {
	name string
	path string
	mode fs.FileMode
}

func activateFleetReleaseArtifacts(updatesDir, fleetID, buildID string) error {
	fleetID = strings.TrimSpace(fleetID)
	buildID = strings.TrimSpace(buildID)

	if !isSafeUpdatePathSegment(fleetID) {
		return fmt.Errorf("invalid fleet identifier")
	}

	if !isSafeUpdatePathSegment(buildID) {
		return fmt.Errorf("invalid build identifier")
	}

	sourceDir := filepath.Join(updatesDir, updatesArtifactsDirName, buildID)
	artifacts, err := collectPublishedArtifacts(sourceDir)
	if err != nil {
		return err
	}

	temporaryFleetDir, err := os.MkdirTemp(updatesDir, ".fleet-"+fleetID+"-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary fleet artifacts directory: %w", err)
	}

	cleanupTemporary := true
	defer func() {
		if cleanupTemporary {
			_ = os.RemoveAll(temporaryFleetDir)
		}
	}()

	for _, artifact := range artifacts {
		mode := artifact.mode
		if mode == 0 {
			mode = 0o644
		}

		destinationPath := filepath.Join(temporaryFleetDir, artifact.name)
		if err := copyFile(artifact.path, destinationPath, mode); err != nil {
			return fmt.Errorf("failed to stage artifact %s for fleet rollout: %w", artifact.name, err)
		}
	}

	targetFleetDir := filepath.Join(updatesDir, fleetID)
	if err := os.RemoveAll(targetFleetDir); err != nil {
		return fmt.Errorf("failed to clear current fleet artifacts: %w", err)
	}

	if err := os.Rename(temporaryFleetDir, targetFleetDir); err != nil {
		return fmt.Errorf("failed to activate fleet artifacts: %w", err)
	}

	cleanupTemporary = false

	return nil
}

func deactivateFleetReleaseArtifacts(updatesDir, fleetID string) error {
	fleetID = strings.TrimSpace(fleetID)

	if !isSafeUpdatePathSegment(fleetID) {
		return fmt.Errorf("invalid fleet identifier")
	}

	targetFleetDir := filepath.Join(updatesDir, fleetID)
	if err := os.RemoveAll(targetFleetDir); err != nil {
		return fmt.Errorf("failed to clear current fleet artifacts: %w", err)
	}

	return nil
}

func collectPublishedArtifacts(directoryPath string) ([]publishedArtifact, error) {
	entries, err := os.ReadDir(directoryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("build artifacts not found")
		}

		return nil, fmt.Errorf("failed to read build artifacts directory: %w", err)
	}

	artifacts := make([]publishedArtifact, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()

		if name == checksumManifestFileName {
			continue
		}

		if !isUpdateArtifactFileName(name) {
			continue
		}

		if entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("failed to read artifact metadata for %s: %w", name, err)
		}

		if !info.Mode().IsRegular() {
			continue
		}

		artifacts = append(artifacts, publishedArtifact{
			name: name,
			path: filepath.Join(directoryPath, name),
			mode: info.Mode().Perm(),
		})
	}

	if len(artifacts) == 0 {
		return nil, fmt.Errorf("build artifacts directory contains no update artifacts")
	}

	sort.Slice(artifacts, func(i, j int) bool {
		return artifacts[i].name < artifacts[j].name
	})

	return artifacts, nil
}

func isSafeUpdatePathSegment(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}

	if strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") {
		return false
	}

	if strings.Contains(trimmed, "..") {
		return false
	}

	return safeUpdatePathSegmentPattern.MatchString(trimmed)
}

func copyFile(sourcePath, destinationPath string, mode fs.FileMode) error {
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}

	defer func() {
		_ = sourceFile.Close()
	}()

	destinationFile, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("failed to open destination file: %w", err)
	}

	defer func() {
		_ = destinationFile.Close()
	}()

	if _, err := io.Copy(destinationFile, sourceFile); err != nil {
		return fmt.Errorf("failed to copy file contents: %w", err)
	}

	return nil
}

func trimBuildOutput(output string, maxBytes int) string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return "no build output"
	}

	if len(trimmed) <= maxBytes {
		return trimmed
	}

	return trimmed[len(trimmed)-maxBytes:]
}
