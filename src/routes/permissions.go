/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"context"
	"strings"

	"github.com/humaidq/fleeti/v2/db"
)

func canViewProfile(user *db.User, profile db.ProfileEdit) bool {
	if user == nil {
		return false
	}

	if user.IsAdmin {
		return true
	}

	ownerUserID := strings.TrimSpace(profile.OwnerUserID)
	if ownerUserID != "" && ownerUserID == user.ID.String() {
		return true
	}

	return strings.TrimSpace(profile.Visibility) == db.VisibilityVisible
}

func canManageProfile(user *db.User, profile db.ProfileEdit) bool {
	if user == nil {
		return false
	}

	if user.IsAdmin {
		return true
	}

	ownerUserID := strings.TrimSpace(profile.OwnerUserID)

	return ownerUserID != "" && ownerUserID == user.ID.String()
}

func canManageFleet(user *db.User, fleet db.Fleet) bool {
	if user == nil {
		return false
	}

	if user.IsAdmin {
		return true
	}

	ownerUserID := strings.TrimSpace(fleet.OwnerUserID)

	return ownerUserID != "" && ownerUserID == user.ID.String()
}

func manageableFleetsForUser(user *db.User, fleets []db.Fleet) []db.Fleet {
	if user == nil {
		return []db.Fleet{}
	}

	if user.IsAdmin {
		return fleets
	}

	filtered := make([]db.Fleet, 0, len(fleets))
	for _, fleet := range fleets {
		if canManageFleet(user, fleet) {
			filtered = append(filtered, fleet)
		}
	}

	return filtered
}

func manageableFleetMap(user *db.User, fleets []db.Fleet) map[string]bool {
	managed := make(map[string]bool, len(fleets))
	for _, fleet := range fleets {
		if !canManageFleet(user, fleet) {
			continue
		}

		managed[fleet.ID] = true
	}

	return managed
}

func ensureUserCanManageFleetIDs(ctx context.Context, user *db.User, fleetIDs []string) error {
	if user == nil {
		return errSessionUserMissing
	}

	if user.IsAdmin {
		return nil
	}

	for _, fleetID := range fleetIDs {
		canManage, err := db.UserCanManageFleet(ctx, user.ID.String(), user.IsAdmin, fleetID)
		if err != nil {
			return err
		}

		if !canManage {
			return db.ErrAccessDenied
		}
	}

	return nil
}
