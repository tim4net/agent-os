-- name: GetEmitterHealth :many
-- Returns per-session liveness state derived from work_events.
-- Pure read — no migration needed (WP-M).
-- Computes liveness as a PURE FUNCTION of (persisted events, server clock now).
-- Contract §4: running requires positive proof (heartbeat within timeout);
-- absence of proof ⇒ stale, never "running" (ADR-001 F10 / ADR-003 D3).
-- Terminal (session.end) is absorbing: once seen, later non-terminal events are
-- ignored for liveness purposes.
-- @stale_window: supervised sessions with no heartbeat in this window are stale.
--   Default = 5 minutes (contract §4).
-- @tenant: empty string returns all tenants; otherwise scopes to one.
WITH session_agg AS (
    -- For each (harness, session_id), compute the session-level state.
    -- IMPORTANT: liveness_mode and host are aggregated per session from
    -- start/heartbeat events, NOT read from the arbitrary latest row.
    -- A note/artifact.created (NULL liveness_mode) as the latest row must
    -- NOT cause a live supervised emitter to be reported stale.
    SELECT DISTINCT ON (harness, session_id)
        harness,
        session_id,
        -- Latest terminal event, if any (session.end).
        MAX(CASE WHEN kind = 'session.end' AND status IN ('done','failed','cancelled')
                 THEN status END) OVER (PARTITION BY harness, session_id)
            AS terminal_status,
        -- Last heartbeat/start received_at (for supervised liveness check).
        MAX(received_at) FILTER (
            WHERE kind IN ('session.start', 'session.heartbeat')
        ) OVER (PARTITION BY harness, session_id)
            AS last_heartbeat,
        -- Latest received_at across all events for this session.
        MAX(received_at) OVER (PARTITION BY harness, session_id)
            AS last_event_received_at,
        -- Latest status seen.
        MAX(status) OVER (PARTITION BY harness, session_id)
            AS last_status,
        -- Tenant from the session (consistent within a session).
        MAX(tenant) OVER (PARTITION BY harness, session_id)
            AS tenant,
        -- Earliest received_at (session start time).
        MIN(received_at) OVER (PARTITION BY harness, session_id)
            AS first_seen,
        -- PID from latest event (if any).
        MAX(pid) OVER (PARTITION BY harness, session_id)
            AS pid,
        -- Per-session liveness_mode: aggregate from start/heartbeat, never from
        -- arbitrary latest row (note/artifact.created legitimately have NULL mode).
        MAX(liveness_mode) FILTER (
            WHERE kind IN ('session.start', 'session.heartbeat')
        ) OVER (PARTITION BY harness, session_id)
            AS session_mode,
        -- Per-session host: derive from start/heartbeat, not from arbitrary latest row.
        MAX(host) FILTER (
            WHERE kind IN ('session.start', 'session.heartbeat')
        ) OVER (PARTITION BY harness, session_id)
            AS session_host
    FROM work_events
    WHERE (sqlc.arg('tenant')::text = '' OR tenant = sqlc.arg('tenant')::text)
    ORDER BY harness, session_id, received_at DESC
),
deduped AS (
    SELECT DISTINCT ON (harness, session_id)
        harness,
        session_id,
        session_host AS host,
        session_mode AS liveness_mode,
        terminal_status,
        last_heartbeat,
        last_event_received_at,
        last_status,
        tenant,
        first_seen,
        pid
    FROM session_agg
    ORDER BY harness, session_id
)
SELECT
    harness,
    session_id,
    host,
    liveness_mode,
    COALESCE(pid::int, 0) AS pid,
    -- Derived liveness state (contract §4):
    CASE
        WHEN terminal_status IS NOT NULL THEN
            -- Terminal seen → absorbing state. Report the terminal status.
            LOWER(terminal_status)
        WHEN liveness_mode = 'supervised' THEN
            CASE
                WHEN last_heartbeat IS NOT NULL
                     AND (NOW() - last_heartbeat) < (sqlc.arg('stale_window')::interval)
                THEN 'running'
                ELSE 'stale'
            END
        WHEN liveness_mode = 'bounded' THEN
            -- Bounded emitters can't heartbeat.
            -- Without host-process-reporter (WP-N), we use age > 6h as stale backstop.
            -- If last event is recent, tentatively 'running' (no proof from reporter yet).
            CASE
                WHEN last_event_received_at IS NOT NULL
                     AND (NOW() - last_event_received_at) < INTERVAL '6 hours'
                THEN 'running'
                ELSE 'stale'
            END
        ELSE 'stale'  -- unknown mode
    END AS status,
    -- Human-readable last-seen (latest received_at from any event).
    last_event_received_at,
    -- For supervised: last heartbeat time specifically.
    last_heartbeat,
    first_seen
FROM deduped
ORDER BY last_event_received_at DESC NULLS LAST
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountEmitterHealthSessions :one
-- Returns total count of distinct (harness, session_id) matching the tenant filter.
SELECT COUNT(DISTINCT (harness, session_id))::bigint AS total
FROM work_events
WHERE (sqlc.arg('tenant')::text = '' OR tenant = sqlc.arg('tenant')::text);
