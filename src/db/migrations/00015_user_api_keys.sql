-- +goose Up

CREATE TABLE IF NOT EXISTS user_api_keys (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key_hash    BYTEA NOT NULL UNIQUE,
    key_prefix  TEXT NOT NULL,
    label       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_user_api_keys_user_id ON user_api_keys(user_id);
CREATE INDEX IF NOT EXISTS idx_user_api_keys_last_used_at ON user_api_keys(last_used_at);

-- +goose Down

DROP INDEX IF EXISTS idx_user_api_keys_last_used_at;
DROP INDEX IF EXISTS idx_user_api_keys_user_id;
DROP TABLE IF EXISTS user_api_keys;
