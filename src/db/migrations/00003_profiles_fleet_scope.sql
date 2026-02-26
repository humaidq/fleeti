-- +goose Up

ALTER TABLE profiles
    ADD COLUMN IF NOT EXISTS fleet_id UUID REFERENCES fleets(id) ON DELETE RESTRICT;

CREATE INDEX IF NOT EXISTS idx_profiles_fleet ON profiles(fleet_id);

-- +goose Down

DROP INDEX IF EXISTS idx_profiles_fleet;

ALTER TABLE profiles
    DROP COLUMN IF EXISTS fleet_id;
