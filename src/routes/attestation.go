/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	tpm2 "github.com/google/go-tpm/legacy/tpm2"

	"github.com/humaidq/fleeti/v2/db"
)

// Quoted PCRs. PCR 11 is the UKI/software-identity measurement (the golden
// anchor, per software version); PCR 7 is the Secure Boot state (per device, only
// meaningful when Secure Boot is enabled).
const (
	attestPCRSoftware  = 11
	attestPCRSecureoot = 7
	attestNonceBytes   = 32
)

// tpmAttestation is the wire form of a TPM quote produced by fleeti-tpm. All byte
// fields are base64; pcrs maps a decimal PCR index to its lowercase-hex sha256
// value. ak_public is only used during registration.
type tpmAttestation struct {
	Attest    string            `json:"attest"`    // base64 TPMS_ATTEST
	Signature string            `json:"signature"` // base64 raw RSASSA signature
	PCRs      map[string]string `json:"pcrs"`      // pcr index -> hex sha256
	AKPublic  string            `json:"ak_public,omitempty"`
}

// newAttestNonce returns a fresh random challenge nonce.
func newAttestNonce() ([]byte, error) {
	nonce := make([]byte, attestNonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("failed to generate attestation nonce: %w", err)
	}

	return nonce, nil
}

// attestationKeyFingerprint is a stable, human-readable digest of an AK public
// area for display.
func attestationKeyFingerprint(akBlob []byte) string {
	digest := sha256.Sum256(akBlob)

	return hex.EncodeToString(digest[:])
}

// errAttestationKeyInvalid marks a malformed attestation key supplied by a device
// (a client error, not an internal failure).
var errAttestationKeyInvalid = errors.New("invalid attestation key")

// decodeAttestationAKPublic decodes and validates a base64-encoded TPMT_PUBLIC for
// an attestation key, returning the raw blob.
func decodeAttestationAKPublic(encoded string) ([]byte, error) {
	akBlob, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("%w: not valid base64: %v", errAttestationKeyInvalid, err)
	}

	pub, err := tpm2.DecodePublic(akBlob)
	if err != nil {
		return nil, fmt.Errorf("%w: not a valid TPM public area: %v", errAttestationKeyInvalid, err)
	}

	if pub.Type != tpm2.AlgRSA {
		return nil, fmt.Errorf("%w: must be RSA", errAttestationKeyInvalid)
	}

	if _, err := pub.Key(); err != nil {
		return nil, fmt.Errorf("%w: public is unusable: %v", errAttestationKeyInvalid, err)
	}

	return akBlob, nil
}

// errQuoteVerification marks a failed-but-expected verification (bad signature,
// nonce mismatch, PCR digest mismatch). It means "not attested", not an internal
// error.
var errQuoteVerification = errors.New("quote verification failed")

// verifyQuote checks that the quote is signed by the AK, that its extraData equals
// the expected nonce, and that the signed PCR digest matches the supplied raw PCR
// values. On success it returns the raw PCR values keyed by index.
func verifyQuote(akBlob []byte, att tpmAttestation, expectedNonce []byte) (map[int][]byte, error) {
	pub, err := tpm2.DecodePublic(akBlob)
	if err != nil {
		return nil, fmt.Errorf("%w: decoding AK public: %v", errQuoteVerification, err)
	}

	key, err := pub.Key()
	if err != nil {
		return nil, fmt.Errorf("%w: deriving AK key: %v", errQuoteVerification, err)
	}

	rsaPub, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%w: AK is not an RSA key", errQuoteVerification)
	}

	attestBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(att.Attest))
	if err != nil {
		return nil, fmt.Errorf("%w: attest is not base64: %v", errQuoteVerification, err)
	}

	sigBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(att.Signature))
	if err != nil {
		return nil, fmt.Errorf("%w: signature is not base64: %v", errQuoteVerification, err)
	}

	digest := sha256.Sum256(attestBytes)
	if err := rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, digest[:], sigBytes); err != nil {
		return nil, fmt.Errorf("%w: AK signature is invalid: %v", errQuoteVerification, err)
	}

	ad, err := tpm2.DecodeAttestationData(attestBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: decoding attestation: %v", errQuoteVerification, err)
	}

	if ad.Type != tpm2.TagAttestQuote || ad.AttestedQuoteInfo == nil {
		return nil, fmt.Errorf("%w: attestation is not a quote", errQuoteVerification)
	}

	if !bytes.Equal(ad.ExtraData, expectedNonce) {
		return nil, fmt.Errorf("%w: quote nonce does not match challenge", errQuoteVerification)
	}

	sel := ad.AttestedQuoteInfo.PCRSelection
	if sel.Hash != tpm2.AlgSHA256 {
		return nil, fmt.Errorf("%w: quote PCR bank is not sha256", errQuoteVerification)
	}

	values, err := parsePCRValues(att.PCRs)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errQuoteVerification, err)
	}

	wantDigest, err := computePCRDigest(sel, values)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errQuoteVerification, err)
	}

	if !bytes.Equal(wantDigest, ad.AttestedQuoteInfo.PCRDigest) {
		return nil, fmt.Errorf("%w: PCR digest does not match supplied PCR values", errQuoteVerification)
	}

	return values, nil
}

// parsePCRValues converts the wire PCR map (index -> hex) into raw bytes.
func parsePCRValues(in map[string]string) (map[int][]byte, error) {
	out := make(map[int][]byte, len(in))
	for key, value := range in {
		idx, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil {
			return nil, fmt.Errorf("invalid PCR index %q", key)
		}

		raw, err := hex.DecodeString(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("invalid PCR %d value", idx)
		}

		out[idx] = raw
	}

	return out, nil
}

// computePCRDigest reproduces the TPM's quoted digest: sha256 over the selected
// PCR values concatenated in ascending index order.
func computePCRDigest(sel tpm2.PCRSelection, values map[int][]byte) ([]byte, error) {
	indices := append([]int(nil), sel.PCRs...)
	sort.Ints(indices)

	h := sha256.New()
	for _, idx := range indices {
		value, ok := values[idx]
		if !ok {
			return nil, fmt.Errorf("missing value for quoted PCR %d", idx)
		}

		h.Write(value)
	}

	return h.Sum(nil), nil
}

// attestationResult reports the outcome of verifying a device telemetry quote.
type attestationResult struct {
	attested bool
	reason   string
}

// verifyDeviceAttestation verifies a telemetry quote against the device's
// registered AK. A device must be explicitly trusted by an admin before it can be
// attested: until then, a verified quote is recorded as "pending" for the
// Trust & Attest action to promote, and the device stays unattested. Once trusted,
// each quote is checked against the golden PCR values. version is the software
// version the device reports.
//
// It returns an attestationResult describing pass/fail (with a human reason) and a
// non-nil error only for internal/DB failures.
func verifyDeviceAttestation(ctx context.Context, device *db.Device, secureBoot bool, version string, att tpmAttestation, expectedNonce []byte) (attestationResult, error) {
	akBlob, _, err := db.GetDeviceAttestationKey(ctx, device.ID)
	if errors.Is(err, db.ErrAttestationKeyNotFound) {
		return attestationResult{reason: "no attestation key registered"}, nil
	}

	if err != nil {
		return attestationResult{}, err
	}

	if len(expectedNonce) == 0 {
		return attestationResult{reason: "no challenge nonce issued yet"}, nil
	}

	values, err := verifyQuote(akBlob, att, expectedNonce)
	if err != nil {
		if errors.Is(err, errQuoteVerification) {
			return attestationResult{reason: err.Error()}, nil
		}

		return attestationResult{}, err
	}

	pcr11, ok := values[attestPCRSoftware]
	if !ok {
		return attestationResult{reason: "quote did not include PCR 11"}, nil
	}

	version = strings.TrimSpace(version)
	if version == "" {
		return attestationResult{reason: "device did not report a software version"}, nil
	}

	var pcr7 []byte
	if secureBoot {
		pcr7, ok = values[attestPCRSecureoot]
		if !ok {
			return attestationResult{reason: "quote did not include PCR 7"}, nil
		}
	}

	trusted, err := db.IsDeviceTrusted(ctx, device.ID)
	if err != nil {
		return attestationResult{}, err
	}

	// Untrusted: the quote is genuine (AK signature + nonce verified), but the
	// device has not been blessed by an admin. Hold the values as pending so the
	// Trust & Attest action can promote them, and leave the device unattested.
	if !trusted {
		if err := db.SetDevicePendingQuote(ctx, device.ID, pcr11, pcr7, version); err != nil {
			return attestationResult{}, err
		}

		return attestationResult{reason: "awaiting admin trust"}, nil
	}

	// Trusted: verify against the established golden values.
	baseline, err := db.GetAttestationBaselinePCR11(ctx, version)
	if errors.Is(err, db.ErrAttestationBaselineNotFound) {
		return attestationResult{reason: "no trusted baseline for this version yet"}, nil
	}

	if err != nil {
		return attestationResult{}, err
	}

	if !bytes.Equal(baseline, pcr11) {
		return attestationResult{reason: "PCR 11 does not match the golden baseline for this version"}, nil
	}

	if secureBoot {
		golden7, err := db.GetDeviceGoldenPCR7(ctx, device.ID)
		if err != nil {
			return attestationResult{}, err
		}

		if len(golden7) == 0 {
			// Secure Boot was enabled after trust; capture the current state.
			if err := db.SetDeviceGoldenPCR7(ctx, device.ID, pcr7); err != nil {
				return attestationResult{}, err
			}

			golden7 = pcr7
		}

		if !bytes.Equal(golden7, pcr7) {
			return attestationResult{reason: "PCR 7 does not match the device's golden Secure Boot state"}, nil
		}
	}

	return attestationResult{attested: true, reason: "ok"}, nil
}

// trustDeviceFromPendingQuote promotes a device's pending verified quote to golden
// and marks it trusted+attested. It establishes the per-version golden PCR 11 (or
// requires a match if one already exists) and records the device's golden PCR 7.
// Returns a human-readable reason when trust cannot proceed.
func trustDeviceFromPendingQuote(ctx context.Context, deviceID string) (string, error) {
	pending, err := db.GetDevicePendingQuote(ctx, deviceID)
	if err != nil {
		return "", err
	}

	if len(pending.PCR11) == 0 || strings.TrimSpace(pending.Version) == "" {
		return "This device has not reported a verified attestation quote yet.", nil
	}

	// Establish the version baseline on first trust, or require the device to match
	// an already-trusted baseline for that version.
	baseline, err := db.EnsureAttestationBaselinePCR11(ctx, pending.Version, pending.PCR11)
	if err != nil {
		return "", err
	}

	if !bytes.Equal(baseline, pending.PCR11) {
		return "This device's boot measurement does not match the trusted baseline already recorded for its software version.", nil
	}

	if err := db.TrustDevice(ctx, deviceID, pending.PCR7); err != nil {
		return "", err
	}

	return "", nil
}

// ensureDeviceNonce returns the device's current challenge nonce, generating and
// persisting one if none has been issued yet.
func ensureDeviceNonce(ctx context.Context, deviceID string) ([]byte, error) {
	nonce, err := db.GetDeviceAttestNonce(ctx, deviceID)
	if err != nil {
		return nil, err
	}

	if len(nonce) > 0 {
		return nonce, nil
	}

	nonce, err = newAttestNonce()
	if err != nil {
		return nil, err
	}

	if err := db.SetDeviceAttestNonce(ctx, deviceID, nonce); err != nil {
		return nil, err
	}

	return nonce, nil
}

// handleTelemetryAttestation verifies an optional attestation block carried by a
// telemetry request, updates the device's attested state, rotates the challenge
// nonce, and returns the hex nonce the device should quote next cycle. When no
// attestation is supplied the device's attested state is left untouched and the
// current nonce is returned unchanged so the device can adopt it.
func handleTelemetryAttestation(ctx context.Context, device *db.Device, secureBoot bool, version string, att *tpmAttestation) (string, error) {
	nonce, err := ensureDeviceNonce(ctx, device.ID)
	if err != nil {
		return "", err
	}

	if att == nil {
		return hex.EncodeToString(nonce), nil
	}

	result, err := verifyDeviceAttestation(ctx, device, secureBoot, version, *att, nonce)
	if err != nil {
		return "", err
	}

	if err := db.SetDeviceAttested(ctx, device.ID, result.attested); err != nil {
		return "", err
	}

	if !result.attested {
		logger.Warn("device attestation failed", "device_id", device.ID, "reason", result.reason)
	}

	// Rotate the nonce after every quote attempt so a captured quote cannot be
	// replayed on a later cycle.
	next, err := newAttestNonce()
	if err != nil {
		return "", err
	}

	if err := db.SetDeviceAttestNonce(ctx, device.ID, next); err != nil {
		return "", err
	}

	return hex.EncodeToString(next), nil
}

// registerDeviceAttestationKey validates and stores a device's AK public, then
// seeds an initial challenge nonce, returning it as hex.
func registerDeviceAttestationKey(ctx context.Context, deviceID, encodedAKPublic string) (string, error) {
	akBlob, err := decodeAttestationAKPublic(encodedAKPublic)
	if err != nil {
		return "", err
	}

	if err := db.RegisterDeviceAttestationKey(ctx, deviceID, akBlob, attestationKeyFingerprint(akBlob)); err != nil {
		return "", err
	}

	nonce, err := newAttestNonce()
	if err != nil {
		return "", err
	}

	if err := db.SetDeviceAttestNonce(ctx, deviceID, nonce); err != nil {
		return "", err
	}

	return hex.EncodeToString(nonce), nil
}
