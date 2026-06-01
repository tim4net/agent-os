-- name: UpsertHostLiveness :one
-- Upserts a host-liveness report keyed on (host, pid).
-- This is the liveness *feed* (WP-N); session-state derivation is a
-- separate consumer concern (WP-J / follow-on WPs).
INSERT INTO host_liveness (host, pid, session_id, harness, cwd, tenant, alive)
VALUES (
    sqlc.arg('host'),
    sqlc.arg('pid'),
    sqlc.arg('session_id'),
    sqlc.arg('harness'),
    sqlc.arg('cwd'),
    sqlc.arg('tenant'),
    sqlc.arg('alive')
)
ON CONFLICT (host, pid)
DO UPDATE SET
    session_id  = EXCLUDED.session_id,
    harness     = EXCLUDED.harness,
    cwd         = EXCLUDED.cwd,
    tenant      = EXCLUDED.tenant,
    alive       = EXCLUDED.alive,
    seen_at     = NOW()
RETURNING *;

-- name: GetHostLiveness :one
-- Fetches a single host-liveness row by (host, pid).
-- Optional tenant filter: returns rows only for matching tenant (or all if empty).
SELECT * FROM host_liveness
WHERE host = sqlc.arg('host') AND pid = sqlc.arg('pid')
AND (sqlc.arg('tenant')::text = '' OR tenant = sqlc.arg('tenant')::text);

-- name: ListHostLiveness :many
-- Lists all liveness records for a tenant, ordered by seen_at DESC.
-- Returns all if tenant is empty.
SELECT * FROM host_liveness
WHERE (sqlc.arg('tenant')::text = '' OR tenant = sqlc.arg('tenant')::text)
ORDER BY seen_at DESC
LIMIT sqlc.arg('lim')::int
OFFSET sqlc.arg('off')::int;

-- name: GetBoundedSessionHostLiveness :one
-- Returns the latest host-liveness alive status for a bounded session.
-- Joins the session's (host, pid) from work_events (kind=session.start) with
-- host_liveness to get the latest alive report. COALESCE(NULL, false) means
-- "no reporter proof → not alive" (contract §4: never running without proof).
--
-- Derivation rule (consumed by deriveBoundedStatus in session_liveness.go):
--   alive=true  → session is running (positive proof from host reporter)
--   alive=false → session is stale (process killed/crashed)
--   no row      → no reporter proof → stale (NEVER running without proof)
--
-- NOTE: seen_at freshness — the reporter only POSTs alive=true on first-seen
-- and cwd-change. Long-running processes do NOT re-POST alive=true. The
-- derivation must account for this (see session_liveness.go BoundedMaxAge
-- backstop and the proposed seen_at TTL in the PR-body wiring diff).
SELECT COALESCE(hl.alive, false)::boolean AS alive
FROM (
    SELECT we_host.host AS host, we_host.pid AS pid
    FROM work_events AS we_host
    WHERE we_host.harness = sqlc.arg('harness')
      AND we_host.session_id = sqlc.arg('session_id')
      AND we_host.tenant = sqlc.arg('tenant')
      AND we_host.kind = 'session.start'
    ORDER BY we_host.received_at DESC
    LIMIT 1
) sess
LEFT JOIN host_liveness hl
    ON hl.host = sess.host AND hl.pid = sess.pid AND hl.tenant = sqlc.arg('tenant');

-- name: CountHostLiveness :one
-- Counts liveness records matching the tenant filter.
SELECT COUNT(*)::bigint FROM host_liveness
WHERE (sqlc.arg('tenant')::text = '' OR tenant = sqlc.arg('tenant')::text);

