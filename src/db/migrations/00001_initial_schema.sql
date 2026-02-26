-- +goose Up

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Shared timestamp updater.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS fleets (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT NOT NULL UNIQUE CHECK (length(trim(name)) > 0),
    description  TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS profiles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE CHECK (length(trim(name)) > 0),
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS profile_revisions (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id              UUID NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    revision                INTEGER NOT NULL CHECK (revision > 0),
    config_schema_version   INTEGER NOT NULL DEFAULT 1 CHECK (config_schema_version > 0),
    config_json             JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(config_json) = 'object'),
    raw_nix                 TEXT NOT NULL DEFAULT '',
    foreign_imports_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    foreign_imports         JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(foreign_imports) = 'array'),
    config_hash             TEXT NOT NULL CHECK (config_hash ~ '^[0-9a-f]{64}$'),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (profile_id, revision),
    UNIQUE (profile_id, config_hash),
    CHECK (foreign_imports_enabled OR foreign_imports = '[]'::jsonb)
);

CREATE TABLE IF NOT EXISTS builds (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_revision_id UUID NOT NULL REFERENCES profile_revisions(id) ON DELETE RESTRICT,
    version             TEXT NOT NULL CHECK (length(trim(version)) > 0),
    status              TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
    artifact_path       TEXT NOT NULL DEFAULT '',
    started_at          TIMESTAMPTZ,
    finished_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (profile_revision_id, version)
);

CREATE TABLE IF NOT EXISTS releases (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    build_id     UUID NOT NULL REFERENCES builds(id) ON DELETE RESTRICT,
    channel      TEXT NOT NULL DEFAULT 'stable',
    version      TEXT NOT NULL UNIQUE CHECK (length(trim(version)) > 0),
    notes        TEXT NOT NULL DEFAULT '',
    published_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS devices (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    fleet_id           UUID NOT NULL REFERENCES fleets(id) ON DELETE RESTRICT,
    hostname           TEXT NOT NULL CHECK (length(trim(hostname)) > 0),
    serial_number      TEXT NOT NULL UNIQUE CHECK (length(trim(serial_number)) > 0),
    current_release_id UUID REFERENCES releases(id) ON DELETE SET NULL,
    desired_release_id UUID REFERENCES releases(id) ON DELETE SET NULL,
    update_state       TEXT NOT NULL DEFAULT 'idle' CHECK (update_state IN ('idle', 'downloading', 'applying', 'rebooting', 'healthy', 'degraded', 'failed')),
    attestation_level  TEXT NOT NULL DEFAULT 'unknown',
    last_seen_at       TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (fleet_id, hostname)
);

CREATE TABLE IF NOT EXISTS rollouts (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    fleet_id      UUID NOT NULL REFERENCES fleets(id) ON DELETE RESTRICT,
    release_id    UUID NOT NULL REFERENCES releases(id) ON DELETE RESTRICT,
    strategy      TEXT NOT NULL DEFAULT 'staged' CHECK (strategy IN ('staged', 'all-at-once')),
    stage_percent INTEGER NOT NULL DEFAULT 10 CHECK (stage_percent >= 1 AND stage_percent <= 100),
    status        TEXT NOT NULL DEFAULT 'planned' CHECK (status IN ('planned', 'in_progress', 'paused', 'completed', 'failed')),
    started_at    TIMESTAMPTZ,
    completed_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_profile_revisions_profile ON profile_revisions(profile_id);
CREATE INDEX IF NOT EXISTS idx_profile_revisions_hash ON profile_revisions(config_hash);
CREATE INDEX IF NOT EXISTS idx_builds_profile_revision ON builds(profile_revision_id);
CREATE INDEX IF NOT EXISTS idx_releases_build ON releases(build_id);
CREATE INDEX IF NOT EXISTS idx_devices_fleet ON devices(fleet_id);
CREATE INDEX IF NOT EXISTS idx_devices_update_state ON devices(update_state);
CREATE INDEX IF NOT EXISTS idx_rollouts_fleet ON rollouts(fleet_id);
CREATE INDEX IF NOT EXISTS idx_rollouts_release ON rollouts(release_id);
CREATE INDEX IF NOT EXISTS idx_rollouts_status ON rollouts(status);

DROP TRIGGER IF EXISTS fleets_updated_at ON fleets;
CREATE TRIGGER fleets_updated_at
    BEFORE UPDATE ON fleets
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

DROP TRIGGER IF EXISTS profiles_updated_at ON profiles;
CREATE TRIGGER profiles_updated_at
    BEFORE UPDATE ON profiles
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

DROP TRIGGER IF EXISTS devices_updated_at ON devices;
CREATE TRIGGER devices_updated_at
    BEFORE UPDATE ON devices
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

-- +goose Down

DROP TRIGGER IF EXISTS devices_updated_at ON devices;
DROP TRIGGER IF EXISTS profiles_updated_at ON profiles;
DROP TRIGGER IF EXISTS fleets_updated_at ON fleets;

DROP TABLE IF EXISTS rollouts;
DROP TABLE IF EXISTS devices;
DROP TABLE IF EXISTS releases;
DROP TABLE IF EXISTS builds;
DROP TABLE IF EXISTS profile_revisions;
DROP TABLE IF EXISTS profiles;
DROP TABLE IF EXISTS fleets;

DROP FUNCTION IF EXISTS update_updated_at();
