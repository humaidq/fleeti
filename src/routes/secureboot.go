/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	secureBootDirName      = "secureboot"
	secureBootKeyFileName  = "db.key"
	secureBootCertFileName = "db.crt"
	secureBootGUIDFileName = "guid"
	signScriptName         = "sign-secure-boot.sh"

	secureBootKeyBits     = 2048
	secureBootCertYears   = 10
	signImageMode         = "image"
	signUpdatePackageMode = "update-package"
)

// secureBootKeyMu serializes per-profile key material generation so concurrent
// builds for the same profile cannot race and produce mismatched key/cert pairs.
var secureBootKeyMu sync.Mutex

// secureBootMaterial points at the on-disk private key, public certificate, and
// owner GUID for a profile's single Secure Boot key.
type secureBootMaterial struct {
	keyPath  string
	certPath string
	guidPath string
}

// resolveSecureBootDirectory returns the root directory holding per-profile
// Secure Boot key material. It mirrors resolveUpdatesDirectory and lives next to
// the updates directory under the service state directory (/var/lib/fleeti at
// runtime). This directory is never referenced by Nix, so the private keys stay
// out of the Nix store/cache.
func resolveSecureBootDirectory() (string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to resolve working directory: %w", err)
	}

	secureBootDir := filepath.Join(workingDir, secureBootDirName)
	if err := os.MkdirAll(secureBootDir, 0o700); err != nil {
		return "", fmt.Errorf("failed to create secure boot directory: %w", err)
	}

	return secureBootDir, nil
}

// ensureProfileSecureBootMaterial returns the Secure Boot key material for a
// profile, generating a fresh RSA key, self-signed certificate, and owner GUID
// on first use. It is idempotent: once generated, the same material is reused for
// every subsequent build of the profile (one key per profile).
func ensureProfileSecureBootMaterial(profileID, profileName string) (secureBootMaterial, error) {
	profileID = strings.TrimSpace(profileID)
	if !isSafeUpdatePathSegment(profileID) {
		return secureBootMaterial{}, fmt.Errorf("invalid profile identifier for secure boot material")
	}

	secureBootDir, err := resolveSecureBootDirectory()
	if err != nil {
		return secureBootMaterial{}, err
	}

	profileDir := filepath.Join(secureBootDir, profileID)
	material := secureBootMaterial{
		keyPath:  filepath.Join(profileDir, secureBootKeyFileName),
		certPath: filepath.Join(profileDir, secureBootCertFileName),
		guidPath: filepath.Join(profileDir, secureBootGUIDFileName),
	}

	secureBootKeyMu.Lock()
	defer secureBootKeyMu.Unlock()

	if secureBootMaterialExists(material) {
		return material, nil
	}

	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return secureBootMaterial{}, fmt.Errorf("failed to create profile secure boot directory: %w", err)
	}

	if err := generateSecureBootMaterial(material, profileID, profileName); err != nil {
		return secureBootMaterial{}, err
	}

	return material, nil
}

func secureBootMaterialExists(material secureBootMaterial) bool {
	for _, path := range []string{material.keyPath, material.certPath, material.guidPath} {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
	}

	return true
}

func generateSecureBootMaterial(material secureBootMaterial, profileID, profileName string) error {
	key, err := rsa.GenerateKey(rand.Reader, secureBootKeyBits)
	if err != nil {
		return fmt.Errorf("failed to generate secure boot key: %w", err)
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return fmt.Errorf("failed to generate certificate serial number: %w", err)
	}

	commonName := strings.TrimSpace(profileName)
	subjectCN := "Fleeti Secure Boot: " + profileID
	if commonName != "" {
		subjectCN = fmt.Sprintf("Fleeti Secure Boot: %s (%s)", commonName, profileID)
	}

	now := time.Now().UTC()
	certTemplate := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   subjectCN,
			Organization: []string{"Fleeti"},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(secureBootCertYears, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &certTemplate, &certTemplate, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("failed to create secure boot certificate: %w", err)
	}

	guid, err := randomGUID()
	if err != nil {
		return err
	}

	// Write the certificate (public) and GUID, then the private key last and with
	// the tightest permissions.
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	if err := writeFileAtomic(material.certPath, certPEM, 0o644); err != nil {
		return fmt.Errorf("failed to write secure boot certificate: %w", err)
	}

	if err := writeFileAtomic(material.guidPath, []byte(guid+"\n"), 0o644); err != nil {
		return fmt.Errorf("failed to write secure boot owner GUID: %w", err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := writeFileAtomic(material.keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("failed to write secure boot private key: %w", err)
	}

	return nil
}

func writeFileAtomic(path string, contents []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}

	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(contents); err != nil {
		_ = tmp.Close()

		return err
	}

	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		return err
	}

	cleanup = false

	return nil
}

func randomGUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("failed to generate owner GUID: %w", err)
	}

	// Format as a RFC 4122 version 4 UUID.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// secureBootCertDetails carries the public certificate information surfaced in
// the profile UI.
type secureBootCertDetails struct {
	PEM         string
	Fingerprint string
	Subject     string
	NotAfter    time.Time
}

// secureBootCertInfo loads and parses a profile's public Secure Boot certificate.
func secureBootCertInfo(material secureBootMaterial) (secureBootCertDetails, error) {
	pemBytes, err := os.ReadFile(material.certPath)
	if err != nil {
		return secureBootCertDetails{}, fmt.Errorf("failed to read secure boot certificate: %w", err)
	}

	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return secureBootCertDetails{}, fmt.Errorf("secure boot certificate is not valid PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return secureBootCertDetails{}, fmt.Errorf("failed to parse secure boot certificate: %w", err)
	}

	return secureBootCertDetails{
		PEM:         string(pemBytes),
		Fingerprint: formatCertFingerprint(cert.Raw),
		Subject:     cert.Subject.CommonName,
		NotAfter:    cert.NotAfter,
	}, nil
}

func formatCertFingerprint(der []byte) string {
	digest := sha256.Sum256(der)
	parts := make([]string, len(digest))
	for i, value := range digest {
		parts[i] = fmt.Sprintf("%02X", value)
	}

	return strings.Join(parts, ":")
}

// signImageArtifact signs the EFI binaries inside a raw disk image's ESP and
// injects Secure Boot auto-enrollment material, using the post-build signing
// script (outside Nix). rawPath must be writable.
func signImageArtifact(ctx context.Context, buildID, scriptPath string, material secureBootMaterial, rawPath string) error {
	return runSignCommand(ctx, buildID, true, scriptPath, signImageMode, rawPath, material.certPath, material.keyPath, material.guidPath)
}

// signUpdatePackageDir signs the UKI(s) inside a published sysupdate package
// directory and refreshes its checksum manifest.
func signUpdatePackageDir(ctx context.Context, buildID, scriptPath string, material secureBootMaterial, dir string) error {
	return runSignCommand(ctx, buildID, false, scriptPath, signUpdatePackageMode, dir, material.certPath, material.keyPath, material.guidPath)
}

func runSignCommand(ctx context.Context, buildID string, installerLogs bool, scriptPath string, args ...string) error {
	scriptPath = strings.TrimSpace(scriptPath)
	if scriptPath == "" {
		return fmt.Errorf("secure boot signing script path is required")
	}

	cmd := exec.CommandContext(ctx, "bash", append([]string{scriptPath}, args...)...)

	var output bytes.Buffer
	logWriter := newPersistentBuildLogWriter(ctx, buildID, installerLogs)
	defer logWriter.Flush()

	multiWriter := io.MultiWriter(&output, logWriter)
	cmd.Stdout = multiWriter
	cmd.Stderr = multiWriter

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("secure boot signing failed: %w: %s", err, trimBuildOutput(output.String(), 8192))
	}

	return nil
}

// signScriptPath returns the path of the signing script inside a build workspace.
func signScriptPath(workspaceNixOSDir string) string {
	return filepath.Join(workspaceNixOSDir, signScriptName)
}
