/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// RegisterDeviceAttestationKey stores (or replaces) a device's TPM attestation
// key public area. Registration is trust-on-first-use: it is only reachable with
// a valid device token, and provisioning happens in a controlled environment, so
// last-write-wins is acceptable. Changing the AK alone does not let an attacker
// pass attestation, because the quote must still match the golden PCR values.
func RegisterDeviceAttestationKey(ctx context.Context, deviceID string, akPublic []byte, fingerprint string) error {
	if pool == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return ErrDeviceNotFound
	}

	if len(akPublic) == 0 {
		return fmt.Errorf("attestation key public area is required")
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO device_attestation_keys (device_id, ak_public, ak_fingerprint)
		VALUES ($1, $2, $3)
		ON CONFLICT (device_id)
		DO UPDATE SET ak_public = excluded.ak_public, ak_fingerprint = excluded.ak_fingerprint
	`, deviceID, akPublic, strings.TrimSpace(fingerprint))
	if err != nil {
		return fmt.Errorf("failed to register attestation key: %w", err)
	}

	return nil
}

// GetDeviceAttestationKey returns the registered AK public area for a device.
func GetDeviceAttestationKey(ctx context.Context, deviceID string) ([]byte, string, error) {
	if pool == nil {
		return nil, "", ErrDatabaseConnectionNotInitialized
	}

	var akPublic []byte
	var fingerprint string
	err := pool.QueryRow(ctx, `
		SELECT ak_public, ak_fingerprint
		FROM device_attestation_keys
		WHERE device_id::text = $1
	`, strings.TrimSpace(deviceID)).Scan(&akPublic, &fingerprint)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrAttestationKeyNotFound
	}

	if err != nil {
		return nil, "", fmt.Errorf("failed to load attestation key: %w", err)
	}

	return akPublic, fingerprint, nil
}

// SetDeviceAttestNonce stores the per-device rolling challenge nonce.
func SetDeviceAttestNonce(ctx context.Context, deviceID string, nonce []byte) error {
	if pool == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	_, err := pool.Exec(ctx, `
		UPDATE devices SET attest_nonce = $2 WHERE id::text = $1
	`, strings.TrimSpace(deviceID), nonce)
	if err != nil {
		return fmt.Errorf("failed to set attestation nonce: %w", err)
	}

	return nil
}

// GetDeviceAttestNonce returns the per-device rolling challenge nonce (nil if
// none has been issued yet).
func GetDeviceAttestNonce(ctx context.Context, deviceID string) ([]byte, error) {
	if pool == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	var nonce []byte
	err := pool.QueryRow(ctx, `
		SELECT attest_nonce FROM devices WHERE id::text = $1
	`, strings.TrimSpace(deviceID)).Scan(&nonce)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDeviceNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("failed to load attestation nonce: %w", err)
	}

	return nonce, nil
}

// GetDeviceGoldenPCR7 returns the device's recorded golden PCR 7 (nil if none).
func GetDeviceGoldenPCR7(ctx context.Context, deviceID string) ([]byte, error) {
	if pool == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	var pcr7 []byte
	err := pool.QueryRow(ctx, `
		SELECT golden_pcr7 FROM devices WHERE id::text = $1
	`, strings.TrimSpace(deviceID)).Scan(&pcr7)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDeviceNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("failed to load golden PCR 7: %w", err)
	}

	return pcr7, nil
}

// SetDeviceGoldenPCR7 records the device's golden PCR 7 (captured once when
// Secure Boot is enabled).
func SetDeviceGoldenPCR7(ctx context.Context, deviceID string, pcr7 []byte) error {
	if pool == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	_, err := pool.Exec(ctx, `
		UPDATE devices SET golden_pcr7 = $2 WHERE id::text = $1
	`, strings.TrimSpace(deviceID), pcr7)
	if err != nil {
		return fmt.Errorf("failed to set golden PCR 7: %w", err)
	}

	return nil
}

// SetDeviceAttested updates the device's attested flag, stamping last_attested_at
// when it passes.
func SetDeviceAttested(ctx context.Context, deviceID string, attested bool) error {
	if pool == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	query := `UPDATE devices SET attested = $2 WHERE id::text = $1`
	if attested {
		query = `UPDATE devices SET attested = $2, last_attested_at = now() WHERE id::text = $1`
	}

	if _, err := pool.Exec(ctx, query, strings.TrimSpace(deviceID), attested); err != nil {
		return fmt.Errorf("failed to set attested flag: %w", err)
	}

	return nil
}

// SetDevicePendingQuote records the most recent verified-but-untrusted quote
// values so the admin "Trust & Attest" action can promote them to golden.
func SetDevicePendingQuote(ctx context.Context, deviceID string, pcr11, pcr7 []byte, version string) error {
	if pool == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	_, err := pool.Exec(ctx, `
		UPDATE devices SET pending_pcr11 = $2, pending_pcr7 = $3, pending_version = $4
		WHERE id::text = $1
	`, strings.TrimSpace(deviceID), pcr11, pcr7, strings.TrimSpace(version))
	if err != nil {
		return fmt.Errorf("failed to set pending quote: %w", err)
	}

	return nil
}

// PendingQuote is a device's last verified-but-untrusted attestation quote.
type PendingQuote struct {
	PCR11   []byte
	PCR7    []byte
	Version string
}

// GetDevicePendingQuote returns the device's pending quote values.
func GetDevicePendingQuote(ctx context.Context, deviceID string) (PendingQuote, error) {
	if pool == nil {
		return PendingQuote{}, ErrDatabaseConnectionNotInitialized
	}

	var pending PendingQuote
	var version *string
	err := pool.QueryRow(ctx, `
		SELECT pending_pcr11, pending_pcr7, pending_version
		FROM devices WHERE id::text = $1
	`, strings.TrimSpace(deviceID)).Scan(&pending.PCR11, &pending.PCR7, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return PendingQuote{}, ErrDeviceNotFound
	}

	if err != nil {
		return PendingQuote{}, fmt.Errorf("failed to load pending quote: %w", err)
	}

	if version != nil {
		pending.Version = *version
	}

	return pending, nil
}

// IsDeviceTrusted reports whether an admin has explicitly trusted the device.
func IsDeviceTrusted(ctx context.Context, deviceID string) (bool, error) {
	if pool == nil {
		return false, ErrDatabaseConnectionNotInitialized
	}

	var trusted bool
	err := pool.QueryRow(ctx, `
		SELECT attest_trusted FROM devices WHERE id::text = $1
	`, strings.TrimSpace(deviceID)).Scan(&trusted)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrDeviceNotFound
	}

	if err != nil {
		return false, fmt.Errorf("failed to load device trust state: %w", err)
	}

	return trusted, nil
}

// TrustDevice marks a device trusted, records its golden PCR 7 (when present),
// stamps it attested, and clears the pending quote. The caller is responsible for
// establishing the per-version golden PCR 11 first.
func TrustDevice(ctx context.Context, deviceID string, goldenPCR7 []byte) error {
	if pool == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	_, err := pool.Exec(ctx, `
		UPDATE devices
		SET attest_trusted = true,
			attested = true,
			last_attested_at = now(),
			golden_pcr7 = COALESCE($2, golden_pcr7),
			pending_pcr11 = NULL,
			pending_pcr7 = NULL,
			pending_version = NULL
		WHERE id::text = $1
	`, strings.TrimSpace(deviceID), goldenPCR7)
	if err != nil {
		return fmt.Errorf("failed to trust device: %w", err)
	}

	return nil
}

// ResetDeviceAttestation clears a device's attestation key, golden PCR 7, nonce,
// and attested flag so the device re-establishes trust on next provisioning.
func ResetDeviceAttestation(ctx context.Context, deviceID string) error {
	if pool == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	deviceID = strings.TrimSpace(deviceID)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin attestation reset: %w", err)
	}

	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM device_attestation_keys WHERE device_id::text = $1`, deviceID); err != nil {
		return fmt.Errorf("failed to clear attestation key: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE devices
		SET attested = false, golden_pcr7 = NULL, attest_nonce = NULL, last_attested_at = NULL,
			attest_trusted = false, pending_pcr11 = NULL, pending_pcr7 = NULL, pending_version = NULL
		WHERE id::text = $1
	`, deviceID); err != nil {
		return fmt.Errorf("failed to clear device attestation state: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit attestation reset: %w", err)
	}

	return nil
}

// EnsureAttestationBaselinePCR11 records the golden PCR 11 for a version on first
// trusted observation and returns the authoritative baseline. If a baseline
// already exists it is returned unchanged (first-write-wins), so callers compare
// the device's value against the returned bytes. The first trusted quote of a
// version therefore establishes the fleet-wide golden for that software.
func EnsureAttestationBaselinePCR11(ctx context.Context, version string, pcr11 []byte) ([]byte, error) {
	if pool == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	version = strings.TrimSpace(version)
	if version == "" {
		return nil, fmt.Errorf("version is required for attestation baseline")
	}

	if len(pcr11) == 0 {
		return nil, fmt.Errorf("PCR 11 value is required for attestation baseline")
	}

	var stored []byte
	err := pool.QueryRow(ctx, `
		INSERT INTO attestation_baselines (version, pcr11)
		VALUES ($1, $2)
		ON CONFLICT (version) DO UPDATE SET version = attestation_baselines.version
		RETURNING pcr11
	`, version, pcr11).Scan(&stored)
	if err != nil {
		return nil, fmt.Errorf("failed to record attestation baseline: %w", err)
	}

	return stored, nil
}

// GetAttestationBaselinePCR11 returns the golden PCR 11 for a version.
func GetAttestationBaselinePCR11(ctx context.Context, version string) ([]byte, error) {
	if pool == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	var pcr11 []byte
	err := pool.QueryRow(ctx, `
		SELECT pcr11 FROM attestation_baselines WHERE version = $1
	`, strings.TrimSpace(version)).Scan(&pcr11)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAttestationBaselineNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("failed to load attestation baseline: %w", err)
	}

	return pcr11, nil
}
