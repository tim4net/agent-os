-- name: UpsertHostLiveness :one
-- Upserts a host-liveness report keyed on (host, pid).
-- If the process is gone (alive=FALSE) and no session.end has been received,
-- the liveness derivation will mark the session as stale (contract §4).
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

-- name: GetHostLivenessBySession :one
-- Fetches the liveness record for a bounded session by session_id.
-- Optional tenant filter: returns rows only for matching tenant (or all if empty).
-- Returns no rows if no reporter covers that session.
SELECT * FROM host_liveness
WHERE session_id = sqlc.arg('session_id') AND session_id != ''
AND (sqlc.arg('tenant')::text = '' OR tenant = sqlc.arg('tenant')::text);

-- name: ListHostLiveness :many
-- Lists all liveness records for a tenant, ordered by seen_at DESC.
-- Returns all if tenant is empty.
SELECT * FROM host_liveness
WHERE (sqlc.arg('tenant')::text = '' OR tenant = sqlc.arg('tenant')::text)
ORDER BY seen_at DESC
LIMIT sqlc.arg('lim')::int
OFFSET sqlc.arg('off')::int;

-- name: CountHostLiveness :one
-- Counts liveness records matching the tenant filter.
SELECT COUNT(*)::bigint FROM host_liveness
WHERE (sqlc.arg('tenant')::text = '' OR tenant = sqlc.arg('tenant')::text);

-- name: ListAliveHostLiveness :many
-- Lists only alive processes for a host. Used by the worktree scanner
-- to correlate worktrees with running bounded processes.
-- Optional tenant filter: returns rows only for matching tenant (or all if empty).
SELECT * FROM host_liveness
WHERE host = sqlc.arg('host') AND alive = TRUE
AND (sqlc.arg('tenant')::text = '' OR tenant = sqlc.arg('tenant')::text);
