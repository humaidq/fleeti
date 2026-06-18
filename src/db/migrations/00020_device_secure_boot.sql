-- +goose Up

-- Secure Boot state reported by the device agent. Drives the reworked
-- attestation tier (none -> secure-boot -> attested). `attested` is reserved
-- for a future TPM challenge and is not set by telemetry yet.
ALTER TABLE devices
    ADD COLUMN IF NOT EXISTS secure_boot_enabled BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS setup_mode          BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS attested            BOOLEAN NOT NULL DEFAULT false;

-- The free-text attestation_level is replaced by a tier computed from the
-- booleans above.
ALTER TABLE devices
    DROP COLUMN IF EXISTS attestation_level;

-- +goose Down

ALTER TABLE devices
    ADD COLUMN IF NOT EXISTS attestation_level TEXT NOT NULL DEFAULT 'unknown';

ALTER TABLE devices
    DROP COLUMN IF EXISTS secure_boot_enabled,
    DROP COLUMN IF EXISTS setup_mode,
    DROP COLUMN IF EXISTS attested;
