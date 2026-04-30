/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flamego/flamego"
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

func TestRenderUpdateChecksumsForDirectoryUsesCompressedArtifactNames(t *testing.T) {
	t.Parallel()

	updatesDir := t.TempDir()
	rawContent := []byte("compressed raw payload")
	ukiContent := []byte("compressed uki payload")

	if err := os.WriteFile(filepath.Join(updatesDir, "fleeti_v1.2.3.nix-store.raw.xz"), rawContent, 0o644); err != nil {
		t.Fatalf("failed to write compressed raw artifact: %v", err)
	}

	if err := os.WriteFile(filepath.Join(updatesDir, "fleeti_v1.2.3.efi.xz"), ukiContent, 0o644); err != nil {
		t.Fatalf("failed to write compressed uki artifact: %v", err)
	}

	checksums, err := renderUpdateChecksumsForDirectory(updatesDir)
	if err != nil {
		t.Fatalf("renderUpdateChecksumsForDirectory returned error: %v", err)
	}

	rawDigest := sha256.Sum256(rawContent)
	if !strings.Contains(string(checksums), hex.EncodeToString(rawDigest[:])+"  fleeti_v1.2.3.nix-store.raw.xz\n") {
		t.Fatalf("expected compressed raw artifact checksum entry, got %q", checksums)
	}

	ukiDigest := sha256.Sum256(ukiContent)
	if !strings.Contains(string(checksums), hex.EncodeToString(ukiDigest[:])+"  fleeti_v1.2.3.efi.xz\n") {
		t.Fatalf("expected compressed uki artifact checksum entry, got %q", checksums)
	}
}

func TestDynamicSHA256SUMSDefersToStaticManifest(t *testing.T) {
	t.Parallel()

	updatesDir := t.TempDir()
	fleetID := "11111111-1111-1111-1111-111111111111"
	fleetDir := filepath.Join(updatesDir, fleetID)
	if err := os.MkdirAll(fleetDir, 0o755); err != nil {
		t.Fatalf("failed to create fleet directory: %v", err)
	}

	checksumPath := filepath.Join(fleetDir, checksumManifestFileName)
	if err := os.WriteFile(checksumPath, []byte("static checksum manifest\n"), 0o644); err != nil {
		t.Fatalf("failed to write static checksum manifest: %v", err)
	}

	app := flamego.New()
	app.Use(DynamicSHA256SUMS(updatesDir))

	nextCalled := false
	app.Get("/update/{fleetID}/SHA256SUMS", func(c flamego.Context) {
		nextCalled = true
		c.ResponseWriter().WriteHeader(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/update/"+fleetID+"/SHA256SUMS", nil)
	app.ServeHTTP(recorder, req)

	if !nextCalled {
		t.Fatal("expected static manifest request to fall through to the next handler")
	}

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, recorder.Code)
	}
}
