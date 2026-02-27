-- +goose Up

ALTER TABLE builds
    DROP CONSTRAINT IF EXISTS builds_profile_revision_id_fleet_id_version_key;

ALTER TABLE builds
    DROP CONSTRAINT IF EXISTS builds_profile_revision_id_version_key;

CREATE UNIQUE INDEX IF NOT EXISTS idx_builds_profile_revision_fleet_version_active
    ON builds(profile_revision_id, fleet_id, version)
    WHERE status IN ('queued', 'running', 'succeeded');

-- +goose Down

DROP INDEX IF EXISTS idx_builds_profile_revision_fleet_version_active;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'builds_profile_revision_id_fleet_id_version_key'
    ) THEN
        ALTER TABLE builds
            ADD CONSTRAINT builds_profile_revision_id_fleet_id_version_key
            UNIQUE (profile_revision_id, fleet_id, version);
    END IF;
END $$;
-- +goose StatementEnd
