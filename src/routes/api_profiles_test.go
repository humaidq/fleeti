/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/humaidq/fleeti/v2/db"
)

func TestNewAPIProfileDetailIncludesParsedConfiguration(t *testing.T) {
	t.Parallel()

	detail, err := newAPIProfileDetail(db.ProfileEdit{
		ID:                  "profile-1",
		FleetID:             "fleet-a",
		FleetIDs:            []string{"fleet-a", "fleet-b"},
		FleetName:           "Fleet A, Fleet B",
		Name:                "Production Base",
		Description:         "Base image for production devices.",
		LatestRevision:      7,
		ConfigHash:          "abc123",
		ConfigSchemaVersion: 1,
		CreatedAt:           "2026-03-19 10:00:00",
		ConfigJSON:          `{"packages":["vim"],"kernel":{"attr":"linux_6_19","source_override":{"enabled":true,"url":"https://github.com/example/linux.git","ref":"refs/heads/main","rev":"abcd1234abcd1234abcd1234abcd1234abcd1234","patches":[{"name":"0001-fix.patch","sha256":"be6f08ca3e84fdbcf9f53c778f1944f24591b31af08c74f81f6ff497f57a5717","content_base64":"ZGlmZiAtLWdpdCBhL2ZvbyBiL2Zvbwo="}]},"ignored":"value"},"openclaw_microvm_enabled":true}`,
		RawNix:              "  { pkgs, ... }: {}  ",
	})
	if err != nil {
		t.Fatalf("newAPIProfileDetail returned error: %v", err)
	}

	if detail.ID != "profile-1" {
		t.Fatalf("expected profile id to be preserved, got %q", detail.ID)
	}

	if len(detail.Packages) != 1 || detail.Packages[0] != "vim" {
		t.Fatalf("expected packages to be parsed, got %#v", detail.Packages)
	}

	if detail.Kernel.Attr != "linux_6_19" {
		t.Fatalf("expected kernel attr to be parsed, got %q", detail.Kernel.Attr)
	}

	if !detail.Kernel.SourceOverride.Enabled {
		t.Fatal("expected kernel source override to be enabled")
	}

	if len(detail.Kernel.SourceOverride.Patches) != 1 {
		t.Fatalf("expected kernel patches to be preserved, got %#v", detail.Kernel.SourceOverride.Patches)
	}

	if !detail.OpenClawMicroVMEnabled {
		t.Fatal("expected openclaw microvm flag to be parsed")
	}

	if strings.TrimSpace(detail.RawNix) != "{ pkgs, ... }: {}" {
		t.Fatalf("expected raw nix to be trimmed, got %q", detail.RawNix)
	}

	if rawKernel, ok := detail.Config["kernel"].(map[string]any); !ok || rawKernel["attr"] != "linux_6_19" {
		t.Fatalf("expected raw config kernel to be preserved, got %#v", detail.Config["kernel"])
	}
}

func TestNewAPIProfileDetailRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	_, err := newAPIProfileDetail(db.ProfileEdit{ConfigJSON: `{"packages":"vim"}`})
	if err == nil {
		t.Fatal("expected invalid config error, got nil")
	}
}

func TestBuildAPIProfilePatchInputAppliesPartialUpdates(t *testing.T) {
	withKernelOptionsCacheForTest([]KernelOption{{Attr: "linux_6_19", Version: "6.19"}}, func() {
		profile := db.ProfileEdit{
			ID:                  "profile-1",
			FleetIDs:            []string{"fleet-a"},
			Name:                "Production Base",
			Description:         "Base image",
			ConfigSchemaVersion: 1,
			ConfigJSON:          `{"packages":["vim"],"custom":{"enabled":false}}`,
			RawNix:              "{ old = true; }",
		}

		input, err := buildAPIProfilePatchInput(context.Background(), profile, apiProfileMutationRequest{
			Config:                 map[string]any{"custom": map[string]any{"enabled": true}},
			Packages:               &[]string{"curl", "git"},
			Kernel:                 &apiProfileKernelConfigInput{Attr: "linux_6_19"},
			OpenClawMicroVMEnabled: boolPtr(true),
			RawNix:                 stringPtr("{ services.example.enable = true; }"),
			ConfigSchemaVersion:    intPtr(2),
		}, map[string]struct{}{"config": {}})
		if err != nil {
			t.Fatalf("buildAPIProfilePatchInput returned error: %v", err)
		}

		packages, err := packagesFromProfileConfig(input.ConfigJSON)
		if err != nil {
			t.Fatalf("packagesFromProfileConfig returned error: %v", err)
		}

		if len(packages) != 2 || packages[0] != "curl" || packages[1] != "git" {
			t.Fatalf("expected packages to be replaced, got %#v", packages)
		}

		kernelConfig, err := profileKernelConfigFromProfileConfig(input.ConfigJSON)
		if err != nil {
			t.Fatalf("profileKernelConfigFromProfileConfig returned error: %v", err)
		}

		if kernelConfig.Attr != "linux_6_19" {
			t.Fatalf("expected kernel attr to be updated, got %q", kernelConfig.Attr)
		}

		enabled, err := openclawMicrovmEnabledFromProfileConfig(input.ConfigJSON)
		if err != nil {
			t.Fatalf("openclawMicrovmEnabledFromProfileConfig returned error: %v", err)
		}

		if !enabled {
			t.Fatal("expected openclaw microvm flag to be enabled")
		}

		config, err := parseProfileConfig(input.ConfigJSON)
		if err != nil {
			t.Fatalf("parseProfileConfig returned error: %v", err)
		}

		custom, ok := config["custom"].(map[string]any)
		if !ok || custom["enabled"] != true {
			t.Fatalf("expected custom config to be merged, got %#v", config["custom"])
		}

		if input.RawNix != "{ services.example.enable = true; }" {
			t.Fatalf("expected raw nix to be updated, got %q", input.RawNix)
		}

		if input.ConfigSchemaVersion != 2 {
			t.Fatalf("expected config schema version to be updated, got %d", input.ConfigSchemaVersion)
		}
	})
}

func TestBuildAPIProfileReplaceInputReplacesConfig(t *testing.T) {
	profile := db.ProfileEdit{
		ID:                  "profile-1",
		FleetIDs:            []string{"fleet-a"},
		Name:                "Production Base",
		Description:         "Base image",
		ConfigSchemaVersion: 1,
		ConfigJSON:          `{"packages":["vim"],"old":true}`,
		RawNix:              "{ old = true; }",
	}

	input, err := buildAPIProfileReplaceInput(context.Background(), profile, apiProfileMutationRequest{
		Config: map[string]any{"packages": []any{"curl"}, "new": true},
	}, map[string]struct{}{"config": {}})
	if err != nil {
		t.Fatalf("buildAPIProfileReplaceInput returned error: %v", err)
	}

	if strings.Contains(input.ConfigJSON, `"old"`) {
		t.Fatalf("expected old config keys to be removed, got %s", input.ConfigJSON)
	}

	if !strings.Contains(input.ConfigJSON, `"new":true`) {
		t.Fatalf("expected replacement config to be stored, got %s", input.ConfigJSON)
	}
}

func TestValidateAPIProfileKernelSelectionRejectsUnavailableKernel(t *testing.T) {
	withKernelOptionsCacheForTest([]KernelOption{{Attr: "linux_6_18", Version: "6.18"}}, func() {
		err := validateAPIProfileKernelSelection(context.Background(), `{"kernel":{"attr":"linux_6_19"}}`)
		var requestErr *apiRequestError
		if !errors.As(err, &requestErr) {
			t.Fatalf("expected request error, got %v", err)
		}

		if requestErr.message != "selected kernel is not available in pinned nixpkgs" {
			t.Fatalf("unexpected request error message: %q", requestErr.message)
		}
	})
}

func TestValidateAPIProfileConfigRejectsUnsupportedWebsiteBlockingCategory(t *testing.T) {
	t.Parallel()

	err := validateAPIProfileConfig(`{"security":{"website_blocking":{"enable":true,"block_categories":["unknown"]}}}`)
	var requestErr *apiRequestError
	if !errors.As(err, &requestErr) {
		t.Fatalf("expected request error, got %v", err)
	}

	if requestErr.message != `website blocking category "unknown" is not supported` {
		t.Fatalf("unexpected request error message: %q", requestErr.message)
	}
}

func withKernelOptionsCacheForTest(options []KernelOption, fn func()) {
	kernelOptionsCacheMu.Lock()
	originalInitialized := kernelOptionsCacheInitialized
	originalOptions := cloneKernelOptions(kernelOptionsCache)
	originalErr := kernelOptionsCacheErr
	kernelOptionsCacheInitialized = true
	kernelOptionsCache = cloneKernelOptions(options)
	kernelOptionsCacheErr = nil
	kernelOptionsCacheMu.Unlock()

	defer func() {
		kernelOptionsCacheMu.Lock()
		kernelOptionsCacheInitialized = originalInitialized
		kernelOptionsCache = originalOptions
		kernelOptionsCacheErr = originalErr
		kernelOptionsCacheMu.Unlock()
	}()

	fn()
}

func boolPtr(value bool) *bool {
	return &value
}

func intPtr(value int) *int {
	return &value
}

func stringPtr(value string) *string {
	return &value
}
