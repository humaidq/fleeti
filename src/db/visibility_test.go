/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package db

import "testing"

func TestNormalizeVisibilityDefaultsToPrivate(t *testing.T) {
	t.Parallel()

	visibility, err := normalizeVisibility("")
	if err != nil {
		t.Fatalf("normalizeVisibility returned error: %v", err)
	}

	if visibility != VisibilityPrivate {
		t.Fatalf("expected %q, got %q", VisibilityPrivate, visibility)
	}
}

func TestNormalizeVisibilityAcceptsVisibleValue(t *testing.T) {
	t.Parallel()

	visibility, err := normalizeVisibility(" visible ")
	if err != nil {
		t.Fatalf("normalizeVisibility returned error: %v", err)
	}

	if visibility != VisibilityVisible {
		t.Fatalf("expected %q, got %q", VisibilityVisible, visibility)
	}
}

func TestNormalizeVisibilityRejectsUnknownValue(t *testing.T) {
	t.Parallel()

	if _, err := normalizeVisibility("shared"); err == nil {
		t.Fatal("expected error for invalid visibility, got nil")
	}
}
