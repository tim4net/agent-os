-- name: InsertWorkEvent :one
-- Upsert by event_id: inserts new row, on conflict does nothing and returns nothing (pgx.ErrNoRows).
INSERT INTO work_events (
    owner_id, event_id, schema_version, harness, session_id, host, pid,
    kind, status, liveness_mode, project_id, tenant,
    external_ref, branch, sha, cwd, title, cost_usd, payload, ts
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11, $12,
    $13, $14, $15, $16, $17, $18, $19, $20
) ON CONFLICT (event_id) DO NOTHING
RETURNING *;

-- name: GetWorkEventByEventID :one
SELECT * FROM work_events WHERE event_id = $1 AND owner_id = $2;

-- name: GetWorkEventsBySession :many
SELECT * FROM work_events
WHERE harness = $1 AND session_id = $2 AND owner_id = $3
ORDER BY received_at DESC
LIMIT $4;
