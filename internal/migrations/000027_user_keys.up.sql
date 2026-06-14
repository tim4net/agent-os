CREATE TABLE user_keys (
    user_id     UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    wrapped_dek BYTEA NOT NULL,
    key_version INT NOT NULL DEFAULT 1,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    rotated_at  TIMESTAMPTZ
);

ALTER TABLE resources ADD COLUMN enc_key_version INT NOT NULL DEFAULT 0;
