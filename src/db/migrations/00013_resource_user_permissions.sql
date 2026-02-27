-- +goose Up

CREATE TABLE IF NOT EXISTS fleet_user_permissions (
    fleet_id    UUID NOT NULL REFERENCES fleets(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    PRIMARY KEY (fleet_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_fleet_user_permissions_user ON fleet_user_permissions(user_id);

CREATE TABLE IF NOT EXISTS profile_user_permissions (
    profile_id  UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    PRIMARY KEY (profile_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_profile_user_permissions_user ON profile_user_permissions(user_id);

-- +goose Down

DROP INDEX IF EXISTS idx_profile_user_permissions_user;
DROP TABLE IF EXISTS profile_user_permissions;

DROP INDEX IF EXISTS idx_fleet_user_permissions_user;
DROP TABLE IF EXISTS fleet_user_permissions;
