/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestEnsureProfileSecureBootMaterialIdempotent(t *testing.T) {
	t.Chdir(t.TempDir())

	profileID := "11111111-1111-4111-8111-111111111111"

	first, err := ensureProfileSecureBootMaterial(profileID, "Test Profile")
	if err != nil {
		t.Fatalf("first ensure failed: %v", err)
	}

	keyBytes1, err := os.ReadFile(first.keyPath)
	if err != nil {
		t.Fatalf("failed to read key: %v", err)
	}

	certBytes1, err := os.ReadFile(first.certPath)
	if err != nil {
		t.Fatalf("failed to read cert: %v", err)
	}

	// A second call (even with a different name) must reuse the existing key.
	second, err := ensureProfileSecureBootMaterial(profileID, "Renamed Profile")
	if err != nil {
		t.Fatalf("second ensure failed: %v", err)
	}

	if first != second {
		t.Fatalf("material paths changed between calls: %+v vs %+v", first, second)
	}

	keyBytes2, _ := os.ReadFile(second.keyPath)
	certBytes2, _ := os.ReadFile(second.certPath)

	if !bytes.Equal(keyBytes1, keyBytes2) {
		t.Error("private key was regenerated on second call")
	}

	if !bytes.Equal(certBytes1, certBytes2) {
		t.Error("certificate was regenerated on second call")
	}

	keyInfo, err := os.Stat(first.keyPath)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}

	if keyInfo.Mode().Perm() != 0o600 {
		t.Errorf("private key mode = %o, want 600", keyInfo.Mode().Perm())
	}

	// The generated key and certificate must be a valid, matching pair.
	keyBlock, _ := pem.Decode(keyBytes1)
	if keyBlock == nil {
		t.Fatal("private key is not valid PEM")
	}

	key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		t.Fatalf("failed to parse private key: %v", err)
	}

	certBlock, _ := pem.Decode(certBytes1)
	if certBlock == nil {
		t.Fatal("certificate is not valid PEM")
	}

	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}

	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) {
		t.Error("certificate public key does not match private key")
	}
}

func TestEnsureProfileSecureBootMaterialRejectsInvalidID(t *testing.T) {
	t.Chdir(t.TempDir())

	for _, id := range []string{"", "../escape", "a/b", ".."} {
		if _, err := ensureProfileSecureBootMaterial(id, "x"); err == nil {
			t.Errorf("expected error for profile id %q", id)
		}
	}
}

func TestSecureBootCertInfo(t *testing.T) {
	t.Chdir(t.TempDir())

	profileID := "22222222-2222-4222-8222-222222222222"

	material, err := ensureProfileSecureBootMaterial(profileID, "Acme Fleet")
	if err != nil {
		t.Fatalf("ensure failed: %v", err)
	}

	details, err := secureBootCertInfo(material)
	if err != nil {
		t.Fatalf("secureBootCertInfo failed: %v", err)
	}

	if !strings.Contains(details.Subject, "Acme Fleet") {
		t.Errorf("subject %q does not contain profile name", details.Subject)
	}

	if !strings.Contains(details.Subject, profileID) {
		t.Errorf("subject %q does not contain profile id", details.Subject)
	}

	fingerprintPattern := regexp.MustCompile(`^([0-9A-F]{2}:){31}[0-9A-F]{2}$`)
	if !fingerprintPattern.MatchString(details.Fingerprint) {
		t.Errorf("fingerprint %q is not a colon-separated SHA-256", details.Fingerprint)
	}

	block, _ := pem.Decode([]byte(details.PEM))
	if block == nil {
		t.Fatal("details.PEM is not valid PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse cert from details.PEM: %v", err)
	}

	if got := fingerprintFromRaw(cert.Raw); got != details.Fingerprint {
		t.Errorf("fingerprint mismatch: details=%q computed=%q", details.Fingerprint, got)
	}

	if details.NotAfter.Before(time.Now().AddDate(9, 0, 0)) {
		t.Errorf("certificate validity too short: NotAfter=%s", details.NotAfter)
	}
}

func TestRandomGUID(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

	first, err := randomGUID()
	if err != nil {
		t.Fatalf("randomGUID failed: %v", err)
	}

	if !pattern.MatchString(first) {
		t.Errorf("guid %q is not a v4 UUID", first)
	}

	second, _ := randomGUID()
	if first == second {
		t.Error("randomGUID returned identical values")
	}
}

func TestRunSignCommandSuccess(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "stub.sh")
	marker := filepath.Join(dir, "marker")

	contents := "#!/usr/bin/env bash\necho \"signing $*\"\ntouch \"" + marker + "\"\nexit 0\n"
	if err := os.WriteFile(script, []byte(contents), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	// installerLogs=false exercises the build-log writer path; with no DB pool it
	// must degrade gracefully rather than panic.
	if err := runSignCommand(context.Background(), "build-1", false, script, signImageMode, "/tmp/example.raw"); err != nil {
		t.Fatalf("runSignCommand returned error: %v", err)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Errorf("stub script did not run (marker missing): %v", err)
	}
}

func TestRunSignCommandFailure(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "stub.sh")

	if err := os.WriteFile(script, []byte("#!/usr/bin/env bash\necho boom >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	err := runSignCommand(context.Background(), "build-1", true, script, signUpdatePackageMode, "/tmp/pkg")
	if err == nil {
		t.Fatal("expected error from failing signing script")
	}

	if !strings.Contains(err.Error(), "secure boot signing failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func fingerprintFromRaw(der []byte) string {
	digest := sha256.Sum256(der)
	parts := make([]string, len(digest))
	for i, value := range digest {
		parts[i] = fmt.Sprintf("%02X", value)
	}

	return strings.Join(parts, ":")
}
