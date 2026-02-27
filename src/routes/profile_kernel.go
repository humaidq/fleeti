/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/humaidq/fleeti/v2/db"
)

const (
	kernelOptionsCommandTimeout = 20 * time.Second
	kernelOptionsFlakeRef       = "github:humaidq/fleeti#nixosConfigurations.fleeti.pkgs.linuxKernel.kernels"
)

var profileKernelAttrPattern = regexp.MustCompile(`^linux_[0-9]+_[0-9]+(_hardened)?$`)

type ProfileKernelSourceOverride struct {
	Enabled bool
	URL     string
	Ref     string
	Rev     string
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

	configValue.SourceOverride = ProfileKernelSourceOverride{
		Enabled: enabled,
		URL:     sourceURL,
		Ref:     sourceRef,
		Rev:     sourceRev,
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

func normalizeProfileKernelConfig(config ProfileKernelConfig) ProfileKernelConfig {
	config.Attr = strings.TrimSpace(config.Attr)
	config.SourceOverride.URL = strings.TrimSpace(config.SourceOverride.URL)
	config.SourceOverride.Ref = strings.TrimSpace(config.SourceOverride.Ref)
	config.SourceOverride.Rev = strings.TrimSpace(config.SourceOverride.Rev)

	if !config.SourceOverride.Enabled {
		config.SourceOverride.URL = ""
		config.SourceOverride.Ref = ""
		config.SourceOverride.Rev = ""
	}

	return config
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
