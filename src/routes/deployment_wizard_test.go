/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"testing"

	"github.com/humaidq/fleeti/v2/db"
)

func TestDeploymentWizardPathIgnoresStateInURL(t *testing.T) {
	t.Parallel()

	path := deploymentWizardPath(deploymentWizardState{
		FleetID:   "fleet-a",
		ProfileID: "profile-a",
		BuildID:   "build-a",
		ReleaseID: "release-a",
	})

	const expected = "/deployments/wizard"
	if path != expected {
		t.Fatalf("expected %q, got %q", expected, path)
	}
}

func TestDeploymentWizardPathWithAnchorAppendsFragment(t *testing.T) {
	t.Parallel()

	path := deploymentWizardPathWithAnchor(deploymentWizardState{FleetID: "fleet-a"}, wizardStepProfileID)

	const expected = "/deployments/wizard#wizard-step-profile"
	if path != expected {
		t.Fatalf("expected %q, got %q", expected, path)
	}
}

func TestProfileAssignedToFleetSupportsLegacyAndMultiFleetFields(t *testing.T) {
	t.Parallel()

	profile := db.Profile{
		FleetID:  "fleet-a",
		FleetIDs: []string{"fleet-a", "fleet-b"},
	}

	if !profileAssignedToFleet(profile, "fleet-a") {
		t.Fatal("expected fleet-a to be assigned")
	}

	if !profileAssignedToFleet(profile, "fleet-b") {
		t.Fatal("expected fleet-b to be assigned")
	}

	if profileAssignedToFleet(profile, "fleet-c") {
		t.Fatal("expected fleet-c to be unassigned")
	}
}
