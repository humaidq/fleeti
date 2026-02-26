-- +goose Up

CREATE TABLE IF NOT EXISTS build_log_chunks (
    id         BIGSERIAL PRIMARY KEY,
    build_id   UUID NOT NULL REFERENCES builds(id) ON DELETE CASCADE,
    chunk      TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_build_log_chunks_build_id_id
    ON build_log_chunks(build_id, id);

-- +goose Down

DROP TABLE IF EXISTS build_log_chunks;
