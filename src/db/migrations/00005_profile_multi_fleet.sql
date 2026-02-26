-- +goose Up

CREATE TABLE IF NOT EXISTS profile_fleets (
    profile_id UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    fleet_id   UUID NOT NULL REFERENCES fleets(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (profile_id, fleet_id)
);

CREATE INDEX IF NOT EXISTS idx_profile_fleets_fleet ON profile_fleets(fleet_id);

INSERT INTO profile_fleets (profile_id, fleet_id)
SELECT id, fleet_id
FROM profiles
WHERE fleet_id IS NOT NULL
ON CONFLICT (profile_id, fleet_id) DO NOTHING;

ALTER TABLE builds
    ADD COLUMN IF NOT EXISTS fleet_id UUID REFERENCES fleets(id) ON DELETE RESTRICT;

UPDATE builds b
SET fleet_id = p.fleet_id
FROM profile_revisions pr
JOIN profiles p ON p.id = pr.profile_id
WHERE b.profile_revision_id = pr.id
  AND b.fleet_id IS NULL;

ALTER TABLE builds
    DROP CONSTRAINT IF EXISTS builds_profile_revision_id_version_key;

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

CREATE INDEX IF NOT EXISTS idx_builds_fleet ON builds(fleet_id);

-- +goose Down

DROP INDEX IF EXISTS idx_builds_fleet;

ALTER TABLE builds
    DROP CONSTRAINT IF EXISTS builds_profile_revision_id_fleet_id_version_key;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'builds_profile_revision_id_version_key'
    ) THEN
        ALTER TABLE builds
            ADD CONSTRAINT builds_profile_revision_id_version_key
            UNIQUE (profile_revision_id, version);
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE builds
    DROP COLUMN IF EXISTS fleet_id;

DROP INDEX IF EXISTS idx_profile_fleets_fleet;

DROP TABLE IF EXISTS profile_fleets;
