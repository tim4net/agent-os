-- name: GetSetting :one
SELECT key, value, is_secret, enc_value, last4, updated_at
FROM app_settings WHERE key = $1;

-- name: ListSettings :many
SELECT key, value, is_secret, enc_value, last4, updated_at
FROM app_settings ORDER BY key;

-- name: UpsertSetting :one
INSERT INTO app_settings (key, value, is_secret, enc_value, last4, updated_at)
VALUES ($1, $2, $3, $4, $5, NOW())
ON CONFLICT (key) DO UPDATE
  SET value = EXCLUDED.value,
      is_secret = EXCLUDED.is_secret,
      enc_value = EXCLUDED.enc_value,
      last4 = EXCLUDED.last4,
      updated_at = NOW()
RETURNING key, value, is_secret, enc_value, last4, updated_at;

-- name: DeleteSetting :exec
DELETE FROM app_settings WHERE key = $1;
