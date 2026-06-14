-- name: GetUserKey :one
SELECT user_id, wrapped_dek, key_version, created_at, rotated_at
FROM user_keys WHERE user_id = $1 AND key_version = 1;

-- name: CreateUserKey :one
INSERT INTO user_keys (user_id, wrapped_dek, key_version)
VALUES ($1, $2, 1)
ON CONFLICT (user_id) DO NOTHING
RETURNING user_id, wrapped_dek, key_version, created_at, rotated_at;

-- name: ListLegacyResources :many
SELECT id, slug, kind, label, provider, is_secret, enc_value, enc_config, config, last4, status, created_at, updated_at, owner_id, enc_key_version
FROM resources WHERE enc_key_version = 0 AND enc_value IS NOT NULL AND length(enc_value) > 0;

-- name: UpdateResourceEncryption :exec
UPDATE resources SET enc_value = $2, enc_key_version = $3, updated_at = NOW() WHERE id = $1;
