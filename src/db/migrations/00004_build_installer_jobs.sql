-- +goose Up

ALTER TABLE builds
    ADD COLUMN IF NOT EXISTS installer_status TEXT NOT NULL DEFAULT 'not_requested'
    CHECK (installer_status IN ('not_requested', 'queued', 'running', 'succeeded', 'failed'));

ALTER TABLE builds
    ADD COLUMN IF NOT EXISTS installer_artifact_path TEXT NOT NULL DEFAULT '';

ALTER TABLE builds
    ADD COLUMN IF NOT EXISTS installer_started_at TIMESTAMPTZ;

ALTER TABLE builds
    ADD COLUMN IF NOT EXISTS installer_finished_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_builds_installer_status ON builds(installer_status);

-- +goose Down

DROP INDEX IF EXISTS idx_builds_installer_status;

ALTER TABLE builds
    DROP COLUMN IF EXISTS installer_finished_at,
    DROP COLUMN IF EXISTS installer_started_at,
    DROP COLUMN IF EXISTS installer_artifact_path,
    DROP COLUMN IF EXISTS installer_status;
