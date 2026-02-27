/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"path/filepath"
	"testing"
)

func TestRenderUpdateChecksumsForDirectoryMissingDirectoryReturnsEmpty(t *testing.T) {
	missingDirectory := filepath.Join(t.TempDir(), "missing")

	checksums, err := renderUpdateChecksumsForDirectory(missingDirectory)
	if err != nil {
		t.Fatalf("renderUpdateChecksumsForDirectory returned error: %v", err)
	}

	if len(checksums) != 0 {
		t.Fatalf("expected empty checksums, got %q", checksums)
	}
}
