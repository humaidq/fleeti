-- +goose Up

DROP INDEX IF EXISTS idx_profiles_owner_visibility;
DROP INDEX IF EXISTS idx_fleets_owner_visibility;

ALTER TABLE profiles
    DROP COLUMN IF EXISTS visibility;

ALTER TABLE fleets
    DROP COLUMN IF EXISTS visibility;

-- +goose Down

ALTER TABLE fleets
    ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'visible'));

ALTER TABLE profiles
    ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'visible'));

CREATE INDEX IF NOT EXISTS idx_fleets_owner_visibility ON fleets(owner_user_id, visibility);
CREATE INDEX IF NOT EXISTS idx_profiles_owner_visibility ON profiles(owner_user_id, visibility);
