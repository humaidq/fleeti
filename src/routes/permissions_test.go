/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"testing"

	"github.com/google/uuid"

	"github.com/humaidq/fleeti/v2/db"
)

func TestCanViewProfileAllowsOwnerAndVisibleProfiles(t *testing.T) {
	t.Parallel()

	owner := &db.User{ID: uuid.New()}
	other := &db.User{ID: uuid.New()}

	privateOwned := db.ProfileEdit{OwnerUserID: owner.ID.String(), Visibility: db.VisibilityPrivate}
	privateForeign := db.ProfileEdit{OwnerUserID: other.ID.String(), Visibility: db.VisibilityPrivate}
	visibleForeign := db.ProfileEdit{OwnerUserID: other.ID.String(), Visibility: db.VisibilityVisible}

	if !canViewProfile(owner, privateOwned) {
		t.Fatal("expected owner to view private profile")
	}

	if canViewProfile(owner, privateForeign) {
		t.Fatal("expected non-owner to be blocked from private profile")
	}

	if !canViewProfile(owner, visibleForeign) {
		t.Fatal("expected visible profile to be viewable")
	}
}

func TestCanManageProfileRequiresOwnerOrAdmin(t *testing.T) {
	t.Parallel()

	owner := &db.User{ID: uuid.New()}
	admin := &db.User{ID: uuid.New(), IsAdmin: true}
	other := &db.User{ID: uuid.New()}

	profile := db.ProfileEdit{OwnerUserID: owner.ID.String(), Visibility: db.VisibilityPrivate}

	if !canManageProfile(owner, profile) {
		t.Fatal("expected owner to manage profile")
	}

	if !canManageProfile(admin, profile) {
		t.Fatal("expected admin to manage profile")
	}

	if canManageProfile(other, profile) {
		t.Fatal("expected non-owner user to be blocked from profile management")
	}
}

func TestManageableFleetsForUserFiltersToOwnedFleets(t *testing.T) {
	t.Parallel()

	owner := &db.User{ID: uuid.New()}
	other := uuid.New().String()

	fleets := []db.Fleet{
		{ID: "fleet-a", OwnerUserID: owner.ID.String(), Visibility: db.VisibilityPrivate},
		{ID: "fleet-b", OwnerUserID: other, Visibility: db.VisibilityVisible},
	}

	filtered := manageableFleetsForUser(owner, fleets)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 fleet, got %d", len(filtered))
	}

	if filtered[0].ID != "fleet-a" {
		t.Fatalf("expected fleet-a, got %s", filtered[0].ID)
	}
}
