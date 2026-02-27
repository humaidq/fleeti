/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"testing"

	"github.com/humaidq/fleeti/v2/db"
)

func TestManageableFleetsForUserReturnsEmptyForNilUser(t *testing.T) {
	t.Parallel()

	fleets := []db.Fleet{{ID: "fleet-a"}, {ID: "fleet-b"}}

	managed := manageableFleetsForUser(nil, fleets)
	if len(managed) != 0 {
		t.Fatalf("expected 0 fleets, got %d", len(managed))
	}
}

func TestManageableFleetsForUserReturnsAllVisibleToUser(t *testing.T) {
	t.Parallel()

	user := &db.User{}

	fleets := []db.Fleet{
		{ID: "fleet-a"},
		{ID: "fleet-b"},
	}

	managed := manageableFleetsForUser(user, fleets)
	if len(managed) != len(fleets) {
		t.Fatalf("expected %d fleets, got %d", len(fleets), len(managed))
	}
}
