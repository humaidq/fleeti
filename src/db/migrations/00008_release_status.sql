-- +goose Up

ALTER TABLE releases
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'withdrawn'));

CREATE INDEX IF NOT EXISTS idx_releases_status ON releases(status);

-- +goose Down

DROP INDEX IF EXISTS idx_releases_status;

ALTER TABLE releases
    DROP COLUMN IF EXISTS status;
