-- name: GetIngestKeyByHash :one
-- Look up a (non-revoked) ingest key by its SHA-256 hash.
-- Returns NULL (pgx.ErrNoRows) if not found or revoked.
SELECT * FROM ingest_keys
WHERE key_hash = $1 AND revoked_at IS NULL;

-- name: CreateIngestKey :one
-- Insert a new ingest key (hashed). The raw key is never stored — callers
-- must hash before calling this.
INSERT INTO ingest_keys (key_hash, tenant, label)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListIngestKeysByTenant :many
-- List all (including revoked) keys for a tenant, newest first.
SELECT * FROM ingest_keys
WHERE tenant = $1
ORDER BY created_at DESC;

-- name: RevokeIngestKey :exec
-- Revoke an ingest key by setting revoked_at to now.
UPDATE ingest_keys SET revoked_at = NOW()
WHERE id = $1 AND revoked_at IS NULL;

-- name: CountActiveIngestKeys :one
-- Count active (non-revoked) keys for a tenant.
SELECT COUNT(*) FROM ingest_keys
WHERE tenant = $1 AND revoked_at IS NULL;
