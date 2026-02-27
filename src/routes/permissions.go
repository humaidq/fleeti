/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"context"

	"github.com/humaidq/fleeti/v2/db"
)

func manageableFleetsForUser(user *db.User, fleets []db.Fleet) []db.Fleet {
	if user == nil {
		return []db.Fleet{}
	}

	return fleets
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
