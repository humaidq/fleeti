/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"strings"
	"testing"

	"github.com/humaidq/fleeti/v2/db"
)

func TestApplyProfileWizardDraftUpdatePreservesUnknownConfig(t *testing.T) {
	t.Parallel()

	draft := profileWizardDraft{
		Name:                "Base",
		Description:         "Old description",
		FleetIDs:            []string{"fleet-a"},
		ConfigJSON:          `{"packages":["vim"],"custom":{"enabled":true}}`,
		RawNix:              "{ old = true; }",
		ConfigSchemaVersion: 1,
	}

	updated, err := applyProfileWizardDraftUpdate(draft, profileWizardDraftUpdateInput{
		Name:                   stringPtr("GPU Lab"),
		Description:            stringPtr("GPU-ready workstation profile"),
		FleetIDs:               &[]string{"fleet-b", "fleet-a"},
		AddPackages:            []string{"git", "htop"},
		KernelAttr:             stringPtr("linux_6_19"),
		OpenClawMicroVMEnabled: boolPtr(true),
		RawNix:                 stringPtr("{ services.openssh.enable = true; }"),
	})
	if err != nil {
		t.Fatalf("applyProfileWizardDraftUpdate returned error: %v", err)
	}

	if updated.Name != "GPU Lab" {
		t.Fatalf("expected name to be updated, got %q", updated.Name)
	}

	if updated.Description != "GPU-ready workstation profile" {
		t.Fatalf("expected description to be updated, got %q", updated.Description)
	}

	if len(updated.FleetIDs) != 2 || updated.FleetIDs[0] != "fleet-a" || updated.FleetIDs[1] != "fleet-b" {
		t.Fatalf("expected fleet ids to be normalized, got %#v", updated.FleetIDs)
	}

	packages, err := packagesFromProfileConfig(updated.ConfigJSON)
	if err != nil {
		t.Fatalf("packagesFromProfileConfig returned error: %v", err)
	}

	if len(packages) != 3 || packages[0] != "vim" || packages[1] != "git" || packages[2] != "htop" {
		t.Fatalf("expected packages to be preserved and extended, got %#v", packages)
	}

	config, err := parseProfileConfig(updated.ConfigJSON)
	if err != nil {
		t.Fatalf("parseProfileConfig returned error: %v", err)
	}

	if _, ok := config["custom"].(map[string]any); !ok {
		t.Fatalf("expected custom config to be preserved, got %#v", config["custom"])
	}

	kernelConfig, err := profileKernelConfigFromProfileConfig(updated.ConfigJSON)
	if err != nil {
		t.Fatalf("profileKernelConfigFromProfileConfig returned error: %v", err)
	}

	if kernelConfig.Attr != "linux_6_19" {
		t.Fatalf("expected kernel attr to be updated, got %q", kernelConfig.Attr)
	}

	openclawEnabled, err := openclawMicrovmEnabledFromProfileConfig(updated.ConfigJSON)
	if err != nil {
		t.Fatalf("openclawMicrovmEnabledFromProfileConfig returned error: %v", err)
	}

	if !openclawEnabled {
		t.Fatal("expected OpenClaw flag to be enabled")
	}

	if strings.TrimSpace(updated.RawNix) != "{ services.openssh.enable = true; }" {
		t.Fatalf("expected raw nix to be updated, got %q", updated.RawNix)
	}
}

func TestApplyProfileWizardDraftUpdateDoesNotClearFieldsFromEmptyToolValues(t *testing.T) {
	t.Parallel()

	draft := profileWizardDraft{
		Name:                "Base",
		Description:         "Existing description",
		FleetIDs:            []string{"fleet-a"},
		ConfigJSON:          `{"packages":["vim","git"],"custom":{"enabled":true}}`,
		RawNix:              "{ old = true; }",
		ConfigSchemaVersion: 1,
	}

	updated, err := applyProfileWizardDraftUpdate(draft, profileWizardDraftUpdateInput{
		Description: stringPtr(""),
		FleetIDs:    &[]string{},
		Packages:    &[]string{},
		RawNix:      stringPtr("{ services.openssh.enable = true; }"),
	})
	if err != nil {
		t.Fatalf("applyProfileWizardDraftUpdate returned error: %v", err)
	}

	if updated.Description != "Existing description" {
		t.Fatalf("expected description to be preserved, got %q", updated.Description)
	}

	if len(updated.FleetIDs) != 1 || updated.FleetIDs[0] != "fleet-a" {
		t.Fatalf("expected fleet ids to be preserved, got %#v", updated.FleetIDs)
	}

	packages, err := packagesFromProfileConfig(updated.ConfigJSON)
	if err != nil {
		t.Fatalf("packagesFromProfileConfig returned error: %v", err)
	}

	if len(packages) != 2 || packages[0] != "vim" || packages[1] != "git" {
		t.Fatalf("expected packages to be preserved, got %#v", packages)
	}

	if strings.TrimSpace(updated.RawNix) != "{ services.openssh.enable = true; }" {
		t.Fatalf("expected raw nix to be updated, got %q", updated.RawNix)
	}
}

func TestApplyProfileWizardDraftUpdateUsesExplicitClearFlags(t *testing.T) {
	t.Parallel()

	draft := profileWizardDraft{
		Name:                "Base",
		Description:         "Existing description",
		FleetIDs:            []string{"fleet-a"},
		ConfigJSON:          `{"packages":["vim","git"]}`,
		RawNix:              "{ old = true; }",
		ConfigSchemaVersion: 1,
	}

	updated, err := applyProfileWizardDraftUpdate(draft, profileWizardDraftUpdateInput{
		ClearDescription: true,
		ClearPackages:    true,
		ClearRawNix:      true,
	})
	if err != nil {
		t.Fatalf("applyProfileWizardDraftUpdate returned error: %v", err)
	}

	if updated.Description != "" {
		t.Fatalf("expected description to be cleared, got %q", updated.Description)
	}

	packages, err := packagesFromProfileConfig(updated.ConfigJSON)
	if err != nil {
		t.Fatalf("packagesFromProfileConfig returned error: %v", err)
	}

	if len(packages) != 0 {
		t.Fatalf("expected packages to be cleared, got %#v", packages)
	}

	if updated.RawNix != "" {
		t.Fatalf("expected raw nix to be cleared, got %q", updated.RawNix)
	}
}

func TestValidateProfileWizardDraftRejectsInvalidSelections(t *testing.T) {
	t.Parallel()

	withKernelOptionsCacheForTest([]KernelOption{{Attr: "linux_6_18", Version: "6.18"}}, func() {
		validation := validateProfileWizardDraft(t.Context(), profileWizardDraft{
			Name:                "",
			FleetIDs:            []string{"fleet-missing"},
			ConfigJSON:          `{"packages":["bad package"],"kernel":{"attr":"linux_6_19"}}`,
			ConfigSchemaVersion: 1,
		}, []db.Fleet{{ID: "fleet-a", Name: "Fleet A"}})

		if validation.CanApply {
			t.Fatal("expected invalid draft to block apply")
		}

		joined := strings.Join(validation.Errors, " | ")
		if !strings.Contains(joined, "Name is required") {
			t.Fatalf("expected name validation error, got %q", joined)
		}

		if !strings.Contains(joined, "Selected fleet is no longer available") {
			t.Fatalf("expected fleet validation error, got %q", joined)
		}

		if !strings.Contains(joined, `Package "bad package" is invalid`) {
			t.Fatalf("expected package validation error, got %q", joined)
		}

		if !strings.Contains(joined, "selected kernel is not available in pinned nixpkgs") {
			t.Fatalf("expected kernel validation error, got %q", joined)
		}
	})
}

func TestNewProfileWizardStateSeedsExistingProfileDraft(t *testing.T) {
	t.Parallel()

	state := newProfileWizardState(profileWizardModeAdapt, db.ProfileEdit{
		ID:                  "profile-1",
		Name:                "Production Base",
		Description:         "Pinned production image",
		FleetIDs:            []string{"fleet-a"},
		ConfigJSON:          `{"packages":["vim"]}`,
		RawNix:              "{ boot.loader.timeout = 1; }",
		ConfigSchemaVersion: 2,
	})

	if state.BaseProfileID != "profile-1" {
		t.Fatalf("expected base profile id to be set, got %q", state.BaseProfileID)
	}

	if state.Draft.ProfileID != "profile-1" {
		t.Fatalf("expected draft profile id to be preserved, got %q", state.Draft.ProfileID)
	}

	if len(state.Conversation) != 1 || !strings.Contains(state.Conversation[0].Content, "Production Base") {
		t.Fatalf("expected seeded assistant message, got %#v", state.Conversation)
	}

	if state.Draft.ConfigSchemaVersion != 2 {
		t.Fatalf("expected config schema version to be preserved, got %d", state.Draft.ConfigSchemaVersion)
	}

	if state.OriginalDraft.Name != "Production Base" {
		t.Fatalf("expected original draft to be seeded, got %#v", state.OriginalDraft)
	}
}

func TestSummarizeProfileWizardDraftIncludesPlannedChangesForAdapt(t *testing.T) {
	t.Parallel()

	summary := summarizeProfileWizardDraft(profileWizardState{
		Mode: profileWizardModeAdapt,
		OriginalDraft: profileWizardDraft{
			ProfileID:           "profile-1",
			Name:                "Production Base",
			Description:         "Pinned production image",
			FleetIDs:            []string{"fleet-a"},
			ConfigJSON:          `{"packages":["vim"],"openclaw_microvm_enabled":true}`,
			RawNix:              "{ old = true; }",
			ConfigSchemaVersion: 1,
		},
		Draft: profileWizardDraft{
			ProfileID:           "profile-1",
			Name:                "GPU Lab",
			Description:         "GPU-ready workstation profile",
			FleetIDs:            []string{"fleet-a", "fleet-b"},
			ConfigJSON:          `{"packages":["git","htop"],"kernel":{"attr":"linux_6_19"}}`,
			RawNix:              "",
			ConfigSchemaVersion: 2,
		},
	}, []db.Fleet{{ID: "fleet-a", Name: "Fleet A"}, {ID: "fleet-b", Name: "Fleet B"}})

	if summary.PlannedChangeCount == 0 {
		t.Fatal("expected planned changes to be reported")
	}

	joined := make([]string, 0, len(summary.PlannedChanges))
	for _, change := range summary.PlannedChanges {
		joined = append(joined, change.Label+": "+change.Detail)
	}
	text := strings.Join(joined, " | ")

	if !strings.Contains(text, "Name: Rename profile to GPU Lab") {
		t.Fatalf("expected rename change, got %q", text)
	}

	if !strings.Contains(text, "Fleets: Assign to Fleet A, Fleet B") {
		t.Fatalf("expected fleet change, got %q", text)
	}

	if !strings.Contains(text, "Packages: Add git, htop; Remove vim") {
		t.Fatalf("expected package change, got %q", text)
	}

	if !strings.Contains(text, "OpenClaw: Disable OpenClaw MicroVM") {
		t.Fatalf("expected openclaw change, got %q", text)
	}

	if !strings.Contains(text, "Raw Nix: Remove raw Nix override") {
		t.Fatalf("expected raw nix removal, got %q", text)
	}
}

func TestSummarizeProfileWizardDraftIncludesPlannedChangesForCreate(t *testing.T) {
	t.Parallel()

	summary := summarizeProfileWizardDraft(profileWizardState{
		Mode: profileWizardModeCreate,
		Draft: profileWizardDraft{
			Name:                "GPU Lab",
			Description:         "GPU-ready workstation profile",
			FleetIDs:            []string{"fleet-a"},
			ConfigJSON:          `{"packages":["git"],"openclaw_microvm_enabled":true}`,
			ConfigSchemaVersion: 1,
		},
	}, []db.Fleet{{ID: "fleet-a", Name: "Fleet A"}})

	if summary.PlannedChangeCount == 0 {
		t.Fatal("expected create summary to show planned changes")
	}

	joined := make([]string, 0, len(summary.PlannedChanges))
	for _, change := range summary.PlannedChanges {
		joined = append(joined, change.Label+": "+change.Detail)
	}
	text := strings.Join(joined, " | ")

	if !strings.Contains(text, "Name: Create profile as GPU Lab") {
		t.Fatalf("expected create name change, got %q", text)
	}

	if !strings.Contains(text, "Fleets: Assign to Fleet A") {
		t.Fatalf("expected fleet assignment, got %q", text)
	}

	if !strings.Contains(text, "Packages: Add git") {
		t.Fatalf("expected package addition, got %q", text)
	}
}
