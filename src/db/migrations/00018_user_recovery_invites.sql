-- +goose Up

ALTER TABLE user_invites
    ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_user_invites_user_id ON user_invites(user_id);

-- +goose Down

DROP INDEX IF EXISTS idx_user_invites_user_id;

ALTER TABLE user_invites
    DROP COLUMN IF EXISTS user_id;
