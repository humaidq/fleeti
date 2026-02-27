-- +goose Up

ALTER TABLE fleets
    ADD COLUMN IF NOT EXISTS owner_user_id UUID REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE fleets
    ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'visible'));

ALTER TABLE profiles
    ADD COLUMN IF NOT EXISTS owner_user_id UUID REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE profiles
    ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'visible'));

CREATE INDEX IF NOT EXISTS idx_fleets_owner_visibility ON fleets(owner_user_id, visibility);
CREATE INDEX IF NOT EXISTS idx_profiles_owner_visibility ON profiles(owner_user_id, visibility);

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM users WHERE is_admin) THEN
        UPDATE fleets
        SET owner_user_id = (
            SELECT id
            FROM users
            WHERE is_admin
            ORDER BY created_at ASC
            LIMIT 1
        )
        WHERE owner_user_id IS NULL;

        UPDATE profiles
        SET owner_user_id = (
            SELECT id
            FROM users
            WHERE is_admin
            ORDER BY created_at ASC
            LIMIT 1
        )
        WHERE owner_user_id IS NULL;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down

DROP INDEX IF EXISTS idx_profiles_owner_visibility;
DROP INDEX IF EXISTS idx_fleets_owner_visibility;

ALTER TABLE profiles
    DROP COLUMN IF EXISTS visibility;

ALTER TABLE profiles
    DROP COLUMN IF EXISTS owner_user_id;

ALTER TABLE fleets
    DROP COLUMN IF EXISTS visibility;

ALTER TABLE fleets
    DROP COLUMN IF EXISTS owner_user_id;
