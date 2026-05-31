-- WP-B correlation engine. Groups work_events into work_units by the correlation key.
-- Per contract §7 the key is (project_id, external_ref, branch, sha); we ALSO group by
-- `tenant` so two tenants emitting the same external_ref with a NULL project_id are never
-- merged into one unit (ADR-002 D6 — employer tenants never co-mingle).
-- Events sharing no code/tracker anchor are surfaced as `correlated=false` units, grouped
-- by their (tenant, project) context — NEVER dropped (ADR-001 F3). The no-drop invariant:
-- SUM(event_count) over all units == COUNT(*) of work_events.

-- name: ListWorkUnits :many
-- Two-level aggregation so liveness is honest (review findings B1/B2/B5):
--   1. session_state: collapse each (key, harness, session_id) to ONE session, taking its
--      latest LIFECYCLE status (only session.start/heartbeat/end drive status — a later
--      artifact.created/note with status='unknown' must NOT mask a terminal session: B1).
--   2. session_live: derive per-session liveness from the SERVER clock NOW() (contract §4:
--      received_at is the only liveness clock — so we compute it here, not on the browser).
--   3. outer: aggregate sessions into the unit with precedence failed>running>stale>done (B2),
--      and SUM per-session cost (cumulative per session) for a correct unit total (B5).
-- Optional tenant filter (B3): @tenant = '' returns all tenants; otherwise scopes to one.
WITH session_state AS (
    SELECT
        tenant, project_id, external_ref, branch, sha, harness, session_id,
        COUNT(*)                          AS sess_events,
        MIN(received_at)                  AS sess_first,
        MAX(received_at)                  AS sess_last,
        (array_agg(status ORDER BY received_at DESC)
            FILTER (WHERE status IS NOT NULL
                    AND kind IN ('session.start', 'session.heartbeat', 'session.end')))[1] AS sess_status,
        (array_agg(title ORDER BY received_at DESC)
            FILTER (WHERE title IS NOT NULL AND title <> ''))[1] AS sess_title,
        (array_agg(kind ORDER BY received_at DESC))[1] AS sess_kind,
        MAX(cost_usd)                     AS sess_cost
    FROM work_events
    WHERE (sqlc.arg(tenant)::text = '' OR tenant = sqlc.arg(tenant)::text)
    GROUP BY tenant, project_id, external_ref, branch, sha, harness, session_id
),
session_live AS (
    SELECT *,
        CASE
            WHEN sess_status = 'failed'              THEN 'failed'
            WHEN sess_status IN ('done', 'cancelled') THEN 'done'
            WHEN NOW() - sess_last > interval '5 minutes' THEN 'stale'
            ELSE 'running'
        END AS sess_liveness
    FROM session_state
)
SELECT
    COALESCE(tenant, '')                  AS tenant,
    COALESCE(project_id::text, '')::text  AS project_key,
    COALESCE(external_ref, '')            AS external_ref,
    COALESCE(branch, '')                  AS branch,
    COALESCE(sha, '')                     AS sha,
    SUM(sess_events)::bigint              AS event_count,
    COUNT(*)::bigint                      AS session_count,
    MIN(sess_first)::timestamptz          AS first_event_at,
    MAX(sess_last)::timestamptz           AS last_event_at,
    (external_ref IS NOT NULL OR branch IS NOT NULL OR sha IS NOT NULL) AS correlated,
    -- Unit liveness with precedence (B2). Computed from the server clock.
    CASE
        WHEN bool_or(sess_liveness = 'failed')  THEN 'failed'
        WHEN bool_or(sess_liveness = 'running') THEN 'running'
        WHEN bool_or(sess_liveness = 'stale')   THEN 'stale'
        ELSE 'done'
    END::text                             AS liveness,
    -- Sessions still alive (running or stale) — lets the card show "N active" honestly.
    COUNT(*) FILTER (WHERE sess_liveness IN ('running', 'stale'))::bigint AS active_session_count,
    COALESCE((array_agg(sess_title ORDER BY sess_last DESC) FILTER (WHERE sess_title IS NOT NULL))[1], '')::text AS title,
    COALESCE((array_agg(sess_kind ORDER BY sess_last DESC))[1], '')::text AS latest_kind,
    (array_agg(DISTINCT harness))::text[] AS harnesses,
    -- SUM of per-session final cost (cumulative per session, contract §6) = correct unit total.
    (SUM(sess_cost))::numeric             AS cost_usd
FROM session_live
GROUP BY tenant, project_id, external_ref, branch, sha
ORDER BY MAX(sess_last) DESC,
         COALESCE(tenant,''), COALESCE(project_id::text,''),
         COALESCE(external_ref,''), COALESCE(branch,''), COALESCE(sha,'')
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: GetWorkUnitEvents :many
-- All events in one group (drill-down). Matches the same 5-part key as ListWorkUnits,
-- NULL-safe so the uncorrelated buckets match rows with NULL key parts.
SELECT *
FROM work_events
WHERE COALESCE(tenant, '')            = $1
  AND COALESCE(project_id::text, '')  = $2
  AND COALESCE(external_ref, '')      = $3
  AND COALESCE(branch, '')            = $4
  AND COALESCE(sha, '')               = $5
ORDER BY received_at ASC;

-- name: CountWorkUnits :one
-- Consistent with ListWorkUnits grouping (same key + same tenant filter) so Total matches.
SELECT COUNT(*) FROM (
    SELECT 1 FROM work_events
    WHERE (sqlc.arg(tenant)::text = '' OR tenant = sqlc.arg(tenant)::text)
    GROUP BY tenant, project_id, external_ref, branch, sha
) g;
