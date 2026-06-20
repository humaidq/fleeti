/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package db

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Foreign imports let a profile pull NixOS modules from external flakes
// (including private ones via an access token) and import a selected subset
// of their `nixosModules` outputs into the built image. They are persisted in
// the scaffolded `profile_revisions.foreign_imports` JSONB column.

const (
	ForeignImportAuthGitHubToken   = "github_token"
	ForeignImportAuthNetrcPassword = "netrc_password"

	// MaxForeignFlakes bounds how many external flakes a single profile may
	// reference, keeping build-time evaluation cost predictable.
	MaxForeignFlakes = 16
	// MaxForeignModulesPerFlake bounds how many modules may be selected from a
	// single flake.
	MaxForeignModulesPerFlake = 64
	// MaxForeignTokenLength bounds the stored access token length.
	MaxForeignTokenLength = 512
)

var (
	// foreignImportRevPattern requires a fully resolved 40-hex git commit so
	// that builtins.getFlake stays pure (no --impure needed at build time).
	foreignImportRevPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	// foreignImportModulePattern restricts module attribute names to a strict
	// single-segment identifier so the generated Nix cannot break out of the
	// quoted attribute selection.
	foreignImportModulePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_'-]*$`)
	// foreignImportHostPattern validates the host portion of an auth entry.
	foreignImportHostPattern = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
)

// ForeignImportAuth carries the credentials used to fetch a private flake.
// The token is stored in plaintext because it must be handed to `nix` at build
// and evaluation time; it is write-only in the UI and must never be logged.
type ForeignImportAuth struct {
	Type     string `json:"type"`
	Host     string `json:"host"`
	Username string `json:"username,omitempty"`
	Token    string `json:"token"`
}

// ForeignImport is a single external flake plus the subset of its nixosModules
// the profile imports. Rev is a pinned commit captured when the flake is added
// or refreshed.
type ForeignImport struct {
	FlakeRef string             `json:"flake_ref"`
	Rev      string             `json:"rev"`
	Modules  []string           `json:"modules"`
	Auth     *ForeignImportAuth `json:"auth,omitempty"`
}

// NormalizeForeignImports trims, de-duplicates, and orders foreign import
// entries so that the stored JSON and the config hash are deterministic for a
// given logical configuration.
func NormalizeForeignImports(imports []ForeignImport) []ForeignImport {
	normalized := make([]ForeignImport, 0, len(imports))

	for _, item := range imports {
		entry := ForeignImport{
			FlakeRef: strings.TrimSpace(item.FlakeRef),
			Rev:      strings.ToLower(strings.TrimSpace(item.Rev)),
		}

		if entry.FlakeRef == "" {
			continue
		}

		seen := map[string]struct{}{}
		modules := make([]string, 0, len(item.Modules))
		for _, module := range item.Modules {
			trimmed := strings.TrimSpace(module)
			if trimmed == "" {
				continue
			}
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			modules = append(modules, trimmed)
		}
		sort.Strings(modules)
		entry.Modules = modules

		if item.Auth != nil {
			token := strings.TrimSpace(item.Auth.Token)
			if token != "" {
				entry.Auth = &ForeignImportAuth{
					Type:     strings.TrimSpace(item.Auth.Type),
					Host:     strings.ToLower(strings.TrimSpace(item.Auth.Host)),
					Username: strings.TrimSpace(item.Auth.Username),
					Token:    token,
				}
			}
		}

		normalized = append(normalized, entry)
	}

	sort.SliceStable(normalized, func(i, j int) bool {
		return normalized[i].FlakeRef < normalized[j].FlakeRef
	})

	return normalized
}

// ValidateForeignImports enforces the safety invariants for foreign imports:
// supported flake reference schemes, pinned revisions, strict module names, and
// configured limits. It is the DB-layer safety net mirrored by the route layer.
func ValidateForeignImports(imports []ForeignImport) error {
	if len(imports) > MaxForeignFlakes {
		return fmt.Errorf("too many external flakes (maximum %d)", MaxForeignFlakes)
	}

	seenRefs := map[string]struct{}{}

	for _, item := range imports {
		if err := validateForeignFlakeRef(item.FlakeRef); err != nil {
			return err
		}

		if _, ok := seenRefs[item.FlakeRef]; ok {
			return fmt.Errorf("external flake %q is listed more than once", item.FlakeRef)
		}
		seenRefs[item.FlakeRef] = struct{}{}

		if !foreignImportRevPattern.MatchString(item.Rev) {
			return fmt.Errorf("external flake %q must be pinned to a resolved commit", item.FlakeRef)
		}

		if len(item.Modules) == 0 {
			return fmt.Errorf("external flake %q must select at least one module", item.FlakeRef)
		}

		if len(item.Modules) > MaxForeignModulesPerFlake {
			return fmt.Errorf("external flake %q selects too many modules (maximum %d)", item.FlakeRef, MaxForeignModulesPerFlake)
		}

		for _, module := range item.Modules {
			if !foreignImportModulePattern.MatchString(module) {
				return fmt.Errorf("invalid module name %q", module)
			}
		}

		if item.Auth != nil {
			if err := validateForeignImportAuth(item.FlakeRef, item.Auth); err != nil {
				return err
			}
		}
	}

	return nil
}

// ValidateForeignFlakeRef checks that a flake reference uses a supported scheme.
// Exposed for the route layer's "load modules" flow before a rev is pinned.
func ValidateForeignFlakeRef(ref string) error {
	return validateForeignFlakeRef(ref)
}

func validateForeignFlakeRef(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return fmt.Errorf("external flake reference is required")
	}

	if strings.Contains(ref, "..") {
		return fmt.Errorf("invalid external flake reference %q", ref)
	}

	switch {
	case strings.HasPrefix(ref, "github:"), strings.HasPrefix(ref, "gitlab:"):
		// Owner/repo style forge references are fetched via access-tokens.
		return nil
	case strings.HasPrefix(ref, "git+https://"):
		return nil
	default:
		return fmt.Errorf("unsupported external flake reference %q (use github:, gitlab:, or git+https://)", ref)
	}
}

func validateForeignImportAuth(ref string, auth *ForeignImportAuth) error {
	switch auth.Type {
	case ForeignImportAuthGitHubToken, ForeignImportAuthNetrcPassword:
	default:
		return fmt.Errorf("unsupported authentication type for external flake %q", ref)
	}

	if auth.Host == "" || !foreignImportHostPattern.MatchString(auth.Host) {
		return fmt.Errorf("invalid authentication host for external flake %q", ref)
	}

	if auth.Token == "" {
		return fmt.Errorf("access token is required for external flake %q", ref)
	}

	if len(auth.Token) > MaxForeignTokenLength {
		return fmt.Errorf("access token for external flake %q is too long", ref)
	}

	return nil
}

// decodeForeignImports parses the stored JSONB array into typed entries.
func decodeForeignImports(raw string) ([]ForeignImport, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil, nil
	}

	var imports []ForeignImport
	if err := json.Unmarshal([]byte(raw), &imports); err != nil {
		return nil, fmt.Errorf("failed to decode foreign imports: %w", err)
	}

	return imports, nil
}

// canonicalizeForeignImports returns the deterministic JSON representation used
// both for the stored JSONB column and for the profile config hash. An empty or
// nil list canonicalizes to "[]" so that profiles without foreign imports keep
// the exact hash material they had before this feature existed.
func canonicalizeForeignImports(imports []ForeignImport) (string, error) {
	if len(imports) == 0 {
		return "[]", nil
	}

	encoded, err := json.Marshal(imports)
	if err != nil {
		return "", fmt.Errorf("failed to canonicalize foreign imports: %w", err)
	}

	return string(encoded), nil
}
