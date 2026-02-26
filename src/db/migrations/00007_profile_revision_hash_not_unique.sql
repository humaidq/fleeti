-- +goose Up

ALTER TABLE profile_revisions
    DROP CONSTRAINT IF EXISTS profile_revisions_profile_id_config_hash_key;

-- +goose Down

ALTER TABLE profile_revisions
    ADD CONSTRAINT profile_revisions_profile_id_config_hash_key
    UNIQUE (profile_id, config_hash);
