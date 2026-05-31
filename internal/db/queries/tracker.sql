-- name: UpsertTrackerItem :one
-- Upsert a tracker item on (project_id, external_ref). Updates title/status/type/url/payload and bumps synced_at.
INSERT INTO tracker_items (
    project_id, external_ref, title, status, item_type, canonical_url, payload, tenant, synced_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, NOW()
) ON CONFLICT (project_id, external_ref) DO UPDATE SET
    title = EXCLUDED.title,
    status = EXCLUDED.status,
    item_type = EXCLUDED.item_type,
    canonical_url = EXCLUDED.canonical_url,
    payload = EXCLUDED.payload,
    synced_at = NOW(),
    updated_at = NOW()
RETURNING *;

-- name: GetTrackerItem :one
SELECT * FROM tracker_items
WHERE project_id = $1 AND external_ref = $2;

-- name: ListTrackerItemsByProject :many
SELECT * FROM tracker_items
WHERE project_id = $1 AND tenant = $2
ORDER BY synced_at DESC
LIMIT $3 OFFSET $4;

-- name: ListTrackerItemsByTenant :many
SELECT * FROM tracker_items
WHERE tenant = $1
ORDER BY synced_at DESC
LIMIT $2 OFFSET $3;

-- name: CountTrackerItemsByTenant :one
SELECT COUNT(*) FROM tracker_items WHERE tenant = $1;

-- name: GetTrackerProjects :many
-- Returns all projects configured with a given tracker type, scoped to a tenant.
SELECT * FROM projects WHERE tracker = $1 AND tenant = $2;
