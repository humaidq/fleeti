-- +goose Up

-- TPM remote attestation. A device proves its boot state by sending a TPM quote
-- over selected PCRs, signed by a TPM-resident attestation key (AK). The AK
-- public is registered once (trust-on-first-use, established in a controlled
-- provisioning environment). PCR 11 (the UKI/software-identity measurement) is
-- the golden anchor and is recorded per software version; PCR 7 (Secure Boot
-- state) is recorded per device when Secure Boot is enabled.

-- One attestation key per device, registered at provisioning.
CREATE TABLE IF NOT EXISTS device_attestation_keys (
    device_id      UUID PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    ak_public      BYTEA NOT NULL,
    ak_fingerprint TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Per-version golden PCR 11. The first trusted quote of a given version
-- establishes the baseline; every later device on that version must match it.
-- Keyed by the version string the agent reports, so it works for any image
-- (published release or test build) without requiring a releases row.
CREATE TABLE IF NOT EXISTS attestation_baselines (
    version    TEXT PRIMARY KEY CHECK (length(trim(version)) > 0),
    pcr11      BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Per-device attestation state.
--   attest_nonce      - the rolling challenge nonce
--   golden_pcr7       - the device's golden PCR 7 (set at trust time when SB is on)
--   last_attested_at  - when the device last passed verification
--   attest_trusted    - whether an admin has explicitly trusted this device
--   pending_pcr11/7   - the most recent VERIFIED but not-yet-trusted quote values,
--                       which the "Trust & Attest" admin action promotes to golden
--   pending_version   - the software version the pending quote was taken on
ALTER TABLE devices
    ADD COLUMN IF NOT EXISTS attest_nonce     BYTEA,
    ADD COLUMN IF NOT EXISTS golden_pcr7      BYTEA,
    ADD COLUMN IF NOT EXISTS last_attested_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS attest_trusted   BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS pending_pcr11    BYTEA,
    ADD COLUMN IF NOT EXISTS pending_pcr7     BYTEA,
    ADD COLUMN IF NOT EXISTS pending_version  TEXT;

-- +goose Down

ALTER TABLE devices
    DROP COLUMN IF EXISTS attest_nonce,
    DROP COLUMN IF EXISTS golden_pcr7,
    DROP COLUMN IF EXISTS last_attested_at,
    DROP COLUMN IF EXISTS attest_trusted,
    DROP COLUMN IF EXISTS pending_pcr11,
    DROP COLUMN IF EXISTS pending_pcr7,
    DROP COLUMN IF EXISTS pending_version;

DROP TABLE IF EXISTS attestation_baselines;
DROP TABLE IF EXISTS device_attestation_keys;
