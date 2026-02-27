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
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/humaidq/fleeti/v2/db"
)

const (
	kernelOptionsCommandTimeout  = 20 * time.Second
	kernelOptionsFlakeRef        = "github:humaidq/fleeti#nixosConfigurations.fleeti.pkgs.linuxKernel.kernels"
	profileKernelPatchFileField  = "kernel_patch_files"
	maxKernelPatchCount          = 32
	maxKernelPatchSizeBytes      = 512 * 1024
	maxKernelPatchTotalSizeBytes = 4 * 1024 * 1024
)

var profileKernelAttrPattern = regexp.MustCompile(`^linux_[0-9]+_[0-9]+(_hardened)?$`)
var profileKernelPatchSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type ProfileKernelPatch struct {
	Name          string
	SHA256        string
	ContentBase64 string
}

type ProfileKernelSourceOverride struct {
	Enabled bool
	URL     string
	Ref     string
	Rev     string
	Patches []ProfileKernelPatch
}

type ProfileKernelConfig struct {
	Attr           string
	SourceOverride ProfileKernelSourceOverride
}

type KernelOption struct {
	Attr    string
	Version string
}

type kernelOptionJSON struct {
	Attr    string `json:"attr"`
	Version string `json:"version"`
}

func listAvailableKernelOptions(ctx context.Context) ([]KernelOption, error) {
	evalCtx, cancel := context.WithTimeout(ctx, kernelOptionsCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(evalCtx,
		"nix",
		"eval",
		"--json",
		kernelOptionsFlakeRef,
		"--apply",
		`kernels: let names = builtins.filter (n: builtins.match "^linux_[0-9]+_[0-9]+(_hardened)?$" n != null) (builtins.attrNames kernels); rows = builtins.map (n: let v = builtins.tryEval (kernels.${n}.version); in if v.success then { attr = n; version = v.value; } else null) names; in builtins.filter (x: x != null) rows`,
	)

	output, err := cmd.Output()
	if err != nil {
		if evalCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("kernel query timed out")
		}

		return nil, fmt.Errorf("failed to evaluate available kernels: %w", err)
	}

	parsed := make([]kernelOptionJSON, 0)
	if err := json.Unmarshal(output, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse kernel query output: %w", err)
	}

	options := make([]KernelOption, 0, len(parsed))
	seen := make(map[string]struct{}, len(parsed))

	for _, item := range parsed {
		attr := strings.TrimSpace(item.Attr)
		if attr == "" {
			continue
		}

		if !profileKernelAttrPattern.MatchString(attr) {
			continue
		}

		if _, exists := seen[attr]; exists {
			continue
		}

		seen[attr] = struct{}{}
		options = append(options, KernelOption{
			Attr:    attr,
			Version: strings.TrimSpace(item.Version),
		})
	}

	sort.Slice(options, func(i, j int) bool {
		return options[i].Attr < options[j].Attr
	})

	return options, nil
}

func ensureKernelOption(options []KernelOption, attr string) []KernelOption {
	trimmed := strings.TrimSpace(attr)
	if trimmed == "" {
		return options
	}

	for _, item := range options {
		if item.Attr == trimmed {
			return options
		}
	}

	updated := append(options, KernelOption{Attr: trimmed})
	sort.Slice(updated, func(i, j int) bool {
		return updated[i].Attr < updated[j].Attr
	})

	return updated
}

func kernelOptionSet(options []KernelOption) map[string]struct{} {
	set := make(map[string]struct{}, len(options))
	for _, option := range options {
		if option.Attr == "" {
			continue
		}

		set[option.Attr] = struct{}{}
	}

	return set
}

func resolveNixOSFlakeDirectory() (string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to resolve working directory: %w", err)
	}

	candidates := []string{
		filepath.Join(workingDir, "nixos"),
		filepath.Join(workingDir, "src", "nixos"),
		filepath.Clean(filepath.Join(workingDir, "..", "nixos")),
		filepath.Clean(filepath.Join(workingDir, "..", "src", "nixos")),
	}

	for _, candidate := range candidates {
		if !directoryContainsFlake(candidate) {
			continue
		}

		return candidate, nil
	}

	return "", fmt.Errorf("failed to locate nixos flake directory")
}

func directoryContainsFlake(path string) bool {
	if path == "" {
		return false
	}

	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}

	flakePath := filepath.Join(path, "flake.nix")
	flakeInfo, err := os.Stat(flakePath)
	if err != nil || flakeInfo.IsDir() {
		return false
	}

	return true
}

func profileKernelConfigFromProfileConfig(configJSON string) (ProfileKernelConfig, error) {
	config, err := parseProfileConfig(configJSON)
	if err != nil {
		return ProfileKernelConfig{}, err
	}

	rawKernel, exists := config["kernel"]
	if !exists || rawKernel == nil {
		return ProfileKernelConfig{}, nil
	}

	decodedKernel, ok := rawKernel.(map[string]any)
	if !ok {
		return ProfileKernelConfig{}, db.ErrInvalidProfileConfigJSON
	}

	attr, err := optionalStringField(decodedKernel, "attr")
	if err != nil {
		return ProfileKernelConfig{}, err
	}

	configValue := ProfileKernelConfig{Attr: attr}

	rawSourceOverride, exists := decodedKernel["source_override"]
	if !exists || rawSourceOverride == nil {
		return normalizeProfileKernelConfig(configValue), nil
	}

	decodedSourceOverride, ok := rawSourceOverride.(map[string]any)
	if !ok {
		return ProfileKernelConfig{}, db.ErrInvalidProfileConfigJSON
	}

	enabled, err := optionalBoolField(decodedSourceOverride, "enabled")
	if err != nil {
		return ProfileKernelConfig{}, err
	}

	sourceURL, err := optionalStringField(decodedSourceOverride, "url")
	if err != nil {
		return ProfileKernelConfig{}, err
	}

	sourceRef, err := optionalStringField(decodedSourceOverride, "ref")
	if err != nil {
		return ProfileKernelConfig{}, err
	}

	sourceRev, err := optionalStringField(decodedSourceOverride, "rev")
	if err != nil {
		return ProfileKernelConfig{}, err
	}

	rawPatches, exists := decodedSourceOverride["patches"]
	patches := make([]ProfileKernelPatch, 0)
	if exists && rawPatches != nil {
		decodedPatches, ok := rawPatches.([]any)
		if !ok {
			return ProfileKernelConfig{}, db.ErrInvalidProfileConfigJSON
		}

		for _, rawPatch := range decodedPatches {
			decodedPatch, ok := rawPatch.(map[string]any)
			if !ok {
				return ProfileKernelConfig{}, db.ErrInvalidProfileConfigJSON
			}

			name, err := optionalStringField(decodedPatch, "name")
			if err != nil {
				return ProfileKernelConfig{}, err
			}

			shaValue, err := optionalStringField(decodedPatch, "sha256")
			if err != nil {
				return ProfileKernelConfig{}, err
			}

			contentBase64, err := optionalStringField(decodedPatch, "content_base64")
			if err != nil {
				return ProfileKernelConfig{}, err
			}

			patches = append(patches, ProfileKernelPatch{
				Name:          name,
				SHA256:        shaValue,
				ContentBase64: contentBase64,
			})
		}
	}

	configValue.SourceOverride = ProfileKernelSourceOverride{
		Enabled: enabled,
		URL:     sourceURL,
		Ref:     sourceRef,
		Rev:     sourceRev,
		Patches: patches,
	}

	return normalizeProfileKernelConfig(configValue), nil
}

func profileKernelConfigFromForm(values url.Values) ProfileKernelConfig {
	enabled := strings.TrimSpace(values.Get("kernel_source_override_enabled")) != ""

	return normalizeProfileKernelConfig(ProfileKernelConfig{
		Attr: strings.TrimSpace(values.Get("kernel_attr")),
		SourceOverride: ProfileKernelSourceOverride{
			Enabled: enabled,
			URL:     strings.TrimSpace(values.Get("kernel_source_url")),
			Ref:     strings.TrimSpace(values.Get("kernel_source_ref")),
			Rev:     strings.TrimSpace(values.Get("kernel_source_rev")),
		},
	})
}

type orderedKernelPatch struct {
	Order    int
	Position int
	Patch    ProfileKernelPatch
}

func profileKernelSourcePatchesFromMultipartForm(values url.Values, form *multipart.Form, existing []ProfileKernelPatch) ([]ProfileKernelPatch, error) {
	files := make([]*multipart.FileHeader, 0)
	if form != nil {
		files = form.File[profileKernelPatchFileField]
	}

	return profileKernelSourcePatchesFromForm(values, files, existing)
}

func profileKernelSourcePatchesFromForm(values url.Values, files []*multipart.FileHeader, existing []ProfileKernelPatch) ([]ProfileKernelPatch, error) {
	ordered := make([]orderedKernelPatch, 0, len(existing)+len(files))
	position := 0
	totalSizeBytes := 0

	for index, patch := range existing {
		if strings.TrimSpace(values.Get(existingKernelPatchRemoveFieldName(index))) != "" {
			continue
		}

		normalizedPatch := normalizeProfileKernelPatch(patch)
		patchSize, err := decodedKernelPatchContentSize(normalizedPatch)
		if err != nil {
			return nil, err
		}

		totalSizeBytes += patchSize
		if totalSizeBytes > maxKernelPatchTotalSizeBytes {
			return nil, fmt.Errorf("kernel patch payload is too large")
		}

		orderValue, err := parseKernelPatchOrder(values.Get(existingKernelPatchOrderFieldName(index)), index+1)
		if err != nil {
			return nil, err
		}

		ordered = append(ordered, orderedKernelPatch{
			Order:    orderValue,
			Position: position,
			Patch:    normalizedPatch,
		})
		position++
	}

	newPatchOrders := values["kernel_new_patch_order"]
	for index, fileHeader := range files {
		patch, err := profileKernelPatchFromUpload(fileHeader)
		if err != nil {
			return nil, err
		}

		patchSize, err := decodedKernelPatchContentSize(patch)
		if err != nil {
			return nil, err
		}

		totalSizeBytes += patchSize
		if totalSizeBytes > maxKernelPatchTotalSizeBytes {
			return nil, fmt.Errorf("kernel patch payload is too large")
		}

		orderDefault := len(existing) + index + 1
		orderValueText := ""
		if index < len(newPatchOrders) {
			orderValueText = newPatchOrders[index]
		}

		orderValue, err := parseKernelPatchOrder(orderValueText, orderDefault)
		if err != nil {
			return nil, err
		}

		ordered = append(ordered, orderedKernelPatch{
			Order:    orderValue,
			Position: position,
			Patch:    patch,
		})
		position++
	}

	if len(ordered) > maxKernelPatchCount {
		return nil, fmt.Errorf("kernel patch count exceeds limit")
	}

	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Order == ordered[j].Order {
			return ordered[i].Position < ordered[j].Position
		}

		return ordered[i].Order < ordered[j].Order
	})

	patches := make([]ProfileKernelPatch, 0, len(ordered))
	for _, entry := range ordered {
		patches = append(patches, entry.Patch)
	}

	return patches, nil
}

func existingKernelPatchOrderFieldName(index int) string {
	return "kernel_existing_patch_order_" + strconv.Itoa(index)
}

func existingKernelPatchRemoveFieldName(index int) string {
	return "kernel_existing_patch_remove_" + strconv.Itoa(index)
}

func parseKernelPatchOrder(value string, fallback int) (int, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback, nil
	}

	parsed, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fmt.Errorf("patch order must be a positive integer")
	}

	if parsed == 0 {
		return fallback, nil
	}

	if parsed < 0 {
		return 0, fmt.Errorf("patch order must be a positive integer")
	}

	return parsed, nil
}

func profileKernelPatchFromUpload(fileHeader *multipart.FileHeader) (ProfileKernelPatch, error) {
	if fileHeader == nil {
		return ProfileKernelPatch{}, fmt.Errorf("kernel patch upload is invalid")
	}

	file, err := fileHeader.Open()
	if err != nil {
		return ProfileKernelPatch{}, fmt.Errorf("failed to open kernel patch upload: %w", err)
	}

	defer func() {
		_ = file.Close()
	}()

	content, err := io.ReadAll(io.LimitReader(file, maxKernelPatchSizeBytes+1))
	if err != nil {
		return ProfileKernelPatch{}, fmt.Errorf("failed to read kernel patch upload: %w", err)
	}

	if len(content) == 0 {
		return ProfileKernelPatch{}, fmt.Errorf("kernel patch upload cannot be empty")
	}

	if len(content) > maxKernelPatchSizeBytes {
		return ProfileKernelPatch{}, fmt.Errorf("kernel patch file is too large")
	}

	name := strings.TrimSpace(filepath.Base(fileHeader.Filename))
	if !isValidProfileKernelPatchName(name) {
		return ProfileKernelPatch{}, fmt.Errorf("kernel patch filename is invalid")
	}

	digest := sha256.Sum256(content)

	return ProfileKernelPatch{
		Name:          name,
		SHA256:        hex.EncodeToString(digest[:]),
		ContentBase64: base64.StdEncoding.EncodeToString(content),
	}, nil
}

func decodedKernelPatchContentSize(patch ProfileKernelPatch) (int, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(patch.ContentBase64))
	if err != nil {
		return 0, fmt.Errorf("kernel patch payload is invalid")
	}

	return len(decoded), nil
}

func normalizeProfileKernelPatch(patch ProfileKernelPatch) ProfileKernelPatch {
	patch.Name = strings.TrimSpace(patch.Name)
	patch.SHA256 = strings.ToLower(strings.TrimSpace(patch.SHA256))
	patch.ContentBase64 = strings.TrimSpace(patch.ContentBase64)

	return patch
}

func isValidProfileKernelPatchName(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return false
	}

	if len(trimmed) > 128 {
		return false
	}

	if strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") {
		return false
	}

	if trimmed == "." || trimmed == ".." {
		return false
	}

	for _, value := range trimmed {
		if value < 32 || value > 126 {
			return false
		}
	}

	return true
}

func normalizeProfileKernelConfig(config ProfileKernelConfig) ProfileKernelConfig {
	config.Attr = strings.TrimSpace(config.Attr)
	config.SourceOverride.URL = strings.TrimSpace(config.SourceOverride.URL)
	config.SourceOverride.Ref = strings.TrimSpace(config.SourceOverride.Ref)
	config.SourceOverride.Rev = strings.TrimSpace(config.SourceOverride.Rev)
	config.SourceOverride.Patches = normalizeProfileKernelPatches(config.SourceOverride.Patches)

	if !config.SourceOverride.Enabled {
		config.SourceOverride.URL = ""
		config.SourceOverride.Ref = ""
		config.SourceOverride.Rev = ""
		config.SourceOverride.Patches = []ProfileKernelPatch{}
	}

	return config
}

func normalizeProfileKernelPatches(patches []ProfileKernelPatch) []ProfileKernelPatch {
	if len(patches) == 0 {
		return []ProfileKernelPatch{}
	}

	normalized := make([]ProfileKernelPatch, 0, len(patches))
	for _, patch := range patches {
		normalized = append(normalized, normalizeProfileKernelPatch(patch))
	}

	return normalized
}

func validateProfileKernelConfig(config ProfileKernelConfig, allowedAttrs map[string]struct{}) error {
	normalized := normalizeProfileKernelConfig(config)

	if normalized.Attr != "" {
		if !profileKernelAttrPattern.MatchString(normalized.Attr) {
			return fmt.Errorf("selected kernel is invalid")
		}

		if len(allowedAttrs) > 0 {
			if _, exists := allowedAttrs[normalized.Attr]; !exists {
				return fmt.Errorf("selected kernel is not available in pinned nixpkgs")
			}
		}
	}

	if !normalized.SourceOverride.Enabled {
		if len(normalized.SourceOverride.Patches) > 0 {
			return fmt.Errorf("kernel patches require source override to be enabled")
		}

		return nil
	}

	if normalized.Attr == "" {
		return fmt.Errorf("kernel source override requires selecting a kernel version")
	}

	if normalized.SourceOverride.URL == "" {
		return fmt.Errorf("kernel source override URL is required")
	}

	if normalized.SourceOverride.Rev == "" {
		return fmt.Errorf("kernel source override revision is required")
	}

	parsedURL, err := url.Parse(normalized.SourceOverride.URL)
	if err != nil || parsedURL == nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return fmt.Errorf("kernel source override URL must be a valid absolute URL")
	}

	if parsedURL.Scheme != "https" && parsedURL.Scheme != "http" {
		return fmt.Errorf("kernel source override URL must use http or https")
	}

	if len(normalized.SourceOverride.Patches) > maxKernelPatchCount {
		return fmt.Errorf("kernel patch count exceeds limit")
	}

	totalSizeBytes := 0
	for _, patch := range normalized.SourceOverride.Patches {
		if !isValidProfileKernelPatchName(patch.Name) {
			return fmt.Errorf("kernel patch filename is invalid")
		}

		if !profileKernelPatchSHA256Pattern.MatchString(patch.SHA256) {
			return fmt.Errorf("kernel patch checksum is invalid")
		}

		content, err := base64.StdEncoding.DecodeString(patch.ContentBase64)
		if err != nil {
			return fmt.Errorf("kernel patch payload is invalid")
		}

		if len(content) == 0 {
			return fmt.Errorf("kernel patch payload cannot be empty")
		}

		if len(content) > maxKernelPatchSizeBytes {
			return fmt.Errorf("kernel patch file is too large")
		}

		digest := sha256.Sum256(content)
		if hex.EncodeToString(digest[:]) != patch.SHA256 {
			return fmt.Errorf("kernel patch checksum mismatch")
		}

		totalSizeBytes += len(content)
		if totalSizeBytes > maxKernelPatchTotalSizeBytes {
			return fmt.Errorf("kernel patch payload is too large")
		}
	}

	return nil
}

func profileConfigWithKernel(configJSON string, kernelConfig ProfileKernelConfig) (string, error) {
	config, err := parseProfileConfig(configJSON)
	if err != nil {
		return "", err
	}

	normalized := normalizeProfileKernelConfig(kernelConfig)

	if normalized.Attr == "" && !normalized.SourceOverride.Enabled {
		delete(config, "kernel")
	} else {
		kernel := map[string]any{
			"attr": normalized.Attr,
		}

		if normalized.SourceOverride.Enabled {
			sourceOverride := map[string]any{
				"enabled": true,
				"url":     normalized.SourceOverride.URL,
				"rev":     normalized.SourceOverride.Rev,
			}

			if normalized.SourceOverride.Ref != "" {
				sourceOverride["ref"] = normalized.SourceOverride.Ref
			}

			if len(normalized.SourceOverride.Patches) > 0 {
				patches := make([]map[string]any, 0, len(normalized.SourceOverride.Patches))
				for _, patch := range normalized.SourceOverride.Patches {
					patches = append(patches, map[string]any{
						"name":           patch.Name,
						"sha256":         patch.SHA256,
						"content_base64": patch.ContentBase64,
					})
				}

				sourceOverride["patches"] = patches
			}

			kernel["source_override"] = sourceOverride
		}

		config["kernel"] = kernel
	}

	encoded, err := json.Marshal(config)
	if err != nil {
		return "", err
	}

	return string(encoded), nil
}

func optionalStringField(values map[string]any, key string) (string, error) {
	raw, exists := values[key]
	if !exists || raw == nil {
		return "", nil
	}

	value, ok := raw.(string)
	if !ok {
		return "", db.ErrInvalidProfileConfigJSON
	}

	return strings.TrimSpace(value), nil
}

func optionalBoolField(values map[string]any, key string) (bool, error) {
	raw, exists := values[key]
	if !exists || raw == nil {
		return false, nil
	}

	value, ok := raw.(bool)
	if !ok {
		return false, db.ErrInvalidProfileConfigJSON
	}

	return value, nil
}
