-- name: ListActiveSessions :many
-- Returns all sessions for a tenant that have a session.start event, ordered by
-- the latest event received_at descending. The caller derives liveness from
-- the returned events in application code (contract §4: pure function of events + clock).
-- Tenant is required (never empty — ADR-002).
SELECT DISTINCT ON (harness, session_id)
    harness,
    session_id,
    host,
    pid,
    liveness_mode,
    tenant,
    kind,
    status,
    received_at,
    ts
FROM work_events
WHERE tenant = $1
  AND kind IN ('session.start', 'session.heartbeat', 'session.end')
ORDER BY harness, session_id, received_at DESC;

-- name: GetSessionTerminalEvent :one
-- Returns the latest session.end event for a (harness, session_id, tenant), if any.
-- Used to check if a session is in a terminal (absorbing) state.
-- Tenant-scoped to prevent cross-tenant absorption (ADR-002).
-- Returns pgx.ErrNoRows if no terminal event exists.
SELECT id, harness, session_id, kind, status, received_at
FROM work_events
WHERE harness = $1
  AND session_id = $2
  AND tenant = $3
  AND kind = 'session.end'
ORDER BY received_at DESC
LIMIT 1;

-- name: GetLatestHeartbeat :one
-- Returns the latest session.start or session.heartbeat received_at for a
-- supervised (harness, session_id, tenant). Used to compute supervised liveness.
-- Tenant-scoped to prevent cross-tenant absorption (ADR-002).
-- Returns pgx.ErrNoRows if no start/heartbeat exists.
SELECT received_at
FROM work_events
WHERE harness = $1
  AND session_id = $2
  AND tenant = $3
  AND kind IN ('session.start', 'session.heartbeat')
ORDER BY received_at DESC
LIMIT 1;

-- name: GetSessionStartTime :one
-- Returns the received_at of the first session.start event for a session.
-- Used to compute bounded_max_age (6h backstop from contract §4).
-- Tenant-scoped to prevent cross-tenant absorption (ADR-002).
-- Returns pgx.ErrNoRows if no session.start exists.
SELECT received_at
FROM work_events
WHERE harness = $1
  AND session_id = $2
  AND tenant = $3
  AND kind = 'session.start'
ORDER BY received_at ASC
LIMIT 1;

-- name: CountSessions :one
-- Counts distinct (harness, session_id) pairs that have at least one
-- session lifecycle event for a given tenant.
SELECT COUNT(DISTINCT (harness || '|' || session_id))::bigint
FROM work_events
WHERE tenant = $1
  AND kind IN ('session.start', 'session.heartbeat', 'session.end');
