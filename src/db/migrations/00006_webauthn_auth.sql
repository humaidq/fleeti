-- +goose Up

CREATE TABLE IF NOT EXISTS users (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    display_name TEXT NOT NULL,
    is_admin     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS user_passkeys (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id   BYTEA NOT NULL UNIQUE,
    credential_data BYTEA NOT NULL,
    label           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_user_passkeys_user_id ON user_passkeys(user_id);
CREATE INDEX IF NOT EXISTS idx_user_passkeys_last_used ON user_passkeys(last_used_at);

CREATE TABLE IF NOT EXISTS user_invites (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token        TEXT NOT NULL UNIQUE,
    display_name TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    used_at      TIMESTAMPTZ,
    created_by   UUID REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_user_invites_used_at ON user_invites(used_at);
CREATE INDEX IF NOT EXISTS idx_user_invites_created_at ON user_invites(created_at DESC);

DROP TRIGGER IF EXISTS users_updated_at ON users;
CREATE TRIGGER users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

-- +goose Down

DROP TRIGGER IF EXISTS users_updated_at ON users;

DROP TABLE IF EXISTS user_invites;
DROP TABLE IF EXISTS user_passkeys;
DROP TABLE IF EXISTS users;
