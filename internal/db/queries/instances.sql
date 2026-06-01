-- name: CreateAppInstance :one
-- Inserts a new app instance. Returns the created row.
INSERT INTO app_instances (harness, session_id, host, pid, label, health_url, branch, sha, cwd, tenant, status)
VALUES (
    sqlc.arg('harness'),
    sqlc.arg('session_id'),
    sqlc.arg('host'),
    sqlc.arg('pid'),
    sqlc.arg('label'),
    sqlc.arg('health_url'),
    sqlc.arg('branch'),
    sqlc.arg('sha'),
    sqlc.arg('cwd'),
    sqlc.arg('tenant'),
    sqlc.arg('status')
)
RETURNING *;

-- name: GetAppInstance :one
-- Fetches a single app instance by ID.
SELECT * FROM app_instances WHERE id = sqlc.arg('id');

-- name: ListAppInstances :many
-- Lists app instances scoped to a tenant. Returns all if tenant is empty.
-- Supports pagination with limit/offset.
-- Orders by last_probed_at DESC NULLS LAST (never-probed instances at end).
SELECT * FROM app_instances
WHERE (sqlc.arg('tenant')::text = '' OR tenant = sqlc.arg('tenant')::text)
ORDER BY
    CASE WHEN status = 'up' THEN 0 ELSE 1 END,
    last_probed_at DESC NULLS LAST,
    created_at DESC
LIMIT sqlc.arg('lim')::int
OFFSET sqlc.arg('off')::int;

-- name: CountAppInstances :one
-- Counts app instances matching the tenant filter (for pagination total).
SELECT COUNT(*)::bigint FROM app_instances
WHERE (sqlc.arg('tenant')::text = '' OR tenant = sqlc.arg('tenant')::text);

-- name: UpsertAppInstanceByHostURL :one
-- Upserts an instance keyed on (host, health_url, tenant).
-- Used by server.started work-event auto-creation: if the same host+url+tenant
-- already exists, updates its session_id, branch, sha, pid, and label.
-- Does NOT change status — only a real probe updates status (anti-fake-status rule).
INSERT INTO app_instances (harness, session_id, host, pid, label, health_url, branch, sha, cwd, tenant, status)
VALUES (
    sqlc.arg('harness'),
    sqlc.arg('session_id'),
    sqlc.arg('host'),
    sqlc.arg('pid'),
    sqlc.arg('label'),
    sqlc.arg('health_url'),
    sqlc.arg('branch'),
    sqlc.arg('sha'),
    sqlc.arg('cwd'),
    sqlc.arg('tenant'),
    'unknown'
)
ON CONFLICT (host, health_url, tenant)
DO UPDATE SET
    session_id  = EXCLUDED.session_id,
    pid         = EXCLUDED.pid,
    branch      = EXCLUDED.branch,
    sha         = EXCLUDED.sha,
    label       = EXCLUDED.label,
    updated_at  = NOW()
WHERE app_instances.host = EXCLUDED.host
  AND app_instances.health_url = EXCLUDED.health_url
  AND app_instances.tenant = EXCLUDED.tenant
RETURNING *;

-- name: UpdateInstanceProbeStatus :exec
-- Updates the status and last_probed_at of an instance after a probe.
-- This is the ONLY way status changes — never set from a work-event.
UPDATE app_instances
SET status        = sqlc.arg('status'),
    last_probed_at = sqlc.arg('last_probed_at'),
    updated_at     = NOW()
WHERE id = sqlc.arg('id');

-- name: UpdateInstanceDown :exec
-- Marks an instance as 'down' (from server.stopped event or probe failure).
UPDATE app_instances
SET status    = 'down',
    updated_at = NOW()
WHERE id = sqlc.arg('id');

-- name: UpsertAppInstanceOnServerStarted :one
-- Called when a server.started work-event arrives (contract §4).
-- Creates the instance if not known, or updates session/branch/sha.
-- Status is set to 'unknown' only on creation (needs a real probe to go 'up').
INSERT INTO app_instances (harness, session_id, host, pid, label, health_url, branch, sha, cwd, tenant, status)
VALUES (
    sqlc.arg('harness'),
    sqlc.arg('session_id'),
    sqlc.arg('host'),
    sqlc.arg('pid'),
    sqlc.arg('label'),
    sqlc.arg('health_url'),
    sqlc.arg('branch'),
    sqlc.arg('sha'),
    sqlc.arg('cwd'),
    sqlc.arg('tenant'),
    'unknown'
)
ON CONFLICT (host, health_url, tenant)
DO UPDATE SET
    session_id   = EXCLUDED.session_id,
    pid          = EXCLUDED.pid,
    branch       = EXCLUDED.branch,
    sha          = EXCLUDED.sha,
    label        = EXCLUDED.label,
    -- Only reset to unknown if currently down (server restarted)
    status       = CASE WHEN app_instances.status = 'down' THEN 'unknown' ELSE app_instances.status END,
    updated_at   = NOW()
WHERE app_instances.host = EXCLUDED.host
  AND app_instances.health_url = EXCLUDED.health_url
  AND app_instances.tenant = EXCLUDED.tenant
RETURNING *;

-- name: MarkInstanceDownByServerStopped :exec
-- Called when a server.stopped work-event arrives (contract §4).
-- Sets status to 'down' — this is a definitive signal, not a probe.
UPDATE app_instances
SET status    = 'down',
    updated_at = NOW()
WHERE host  = sqlc.arg('host')
  AND tenant = sqlc.arg('tenant')
  AND health_url IS NOT NULL
  AND health_url != '';

-- name: DeleteAppInstance :exec
-- Deletes an app instance by ID.
DELETE FROM app_instances WHERE id = sqlc.arg('id');
