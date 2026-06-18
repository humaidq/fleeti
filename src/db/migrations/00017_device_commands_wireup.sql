-- +goose Up

-- Latest available release reported by the device's agent telemetry.
ALTER TABLE devices
    ADD COLUMN IF NOT EXISTS available_version TEXT NOT NULL DEFAULT '';

-- Allow at most one outstanding command per device at a time.
CREATE UNIQUE INDEX IF NOT EXISTS idx_device_commands_one_pending
    ON device_commands(device_id) WHERE status = 'pending';

-- +goose Down

DROP INDEX IF EXISTS idx_device_commands_one_pending;

ALTER TABLE devices
    DROP COLUMN IF EXISTS available_version;
