-- +goose Up

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS last_login_at TIMESTAMPTZ;

-- +goose Down

ALTER TABLE users
    DROP COLUMN IF EXISTS last_login_at;
