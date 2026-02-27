-- +goose Up

CREATE TABLE IF NOT EXISTS flamego_sessions (
    id         TEXT PRIMARY KEY,
    data       BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_flamego_sessions_expires_at ON flamego_sessions(expires_at);

-- +goose Down

DROP TABLE IF EXISTS flamego_sessions;
