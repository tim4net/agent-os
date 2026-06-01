-- name: AggregateSpend :many
-- Aggregates cost_usd + num_turns (from telemetry) from work_events, grouped by the
-- requested dimension (agent/harness, project, tenant, or day). Pure read over
-- existing work_events — no migration needed (WP-K).
-- Cost has ONE source of truth: top-level cost_usd (contract §5).
-- Turns are counted from payload->telemetry->turns (non-null only).
-- Tenant-scoped: @tenant = '' returns all tenants; otherwise scopes to one.
-- external_ref filter: @external_ref = '' returns all; otherwise scopes to one.
-- Per the work-event contract (§5, lines 213-215), cost_usd is "cumulative for the
-- session; non-decreasing" and "latest received_at wins." We dedupe to the latest
-- event per (harness, session_id) before rolling up, so a session that emits
-- start=$0.05 then end=$0.07 contributes $0.07, not $0.12.
WITH latest_per_session AS (
    SELECT DISTINCT ON (harness, session_id)
        harness,
        COALESCE(project_id::text, '') AS project_key,
        tenant,
        ts::date                       AS day,
        cost_usd,
        (payload->'telemetry'->>'turns')::int AS turns,
        external_ref
    FROM work_events
    WHERE cost_usd IS NOT NULL
      AND (sqlc.arg('tenant')::text = '' OR tenant = sqlc.arg('tenant')::text)
      AND (sqlc.arg('external_ref')::text = '' OR external_ref = sqlc.arg('external_ref')::text)
    ORDER BY harness, session_id, received_at DESC
),
spend_source AS (
    SELECT
        harness,
        project_key,
        tenant,
        day,
        cost_usd,
        turns
    FROM latest_per_session
)
SELECT
    CASE sqlc.arg('group_by')
        WHEN 'agent'  THEN harness
        WHEN 'project' THEN project_key
        WHEN 'tenant' THEN tenant
        WHEN 'day'    THEN day::text
        ELSE harness   -- default fallback = agent
    END::text  AS dimension_key,
    SUM(cost_usd)::numeric          AS total_cost_usd,
    COUNT(*)::bigint                AS event_count,
    COALESCE(SUM(COALESCE(turns, 0))::bigint, 0)::bigint AS total_turns,
    COUNT(*) OVER()::bigint          AS total_groups
FROM spend_source
GROUP BY
    CASE sqlc.arg('group_by')
        WHEN 'agent'  THEN harness
        WHEN 'project' THEN project_key
        WHEN 'tenant' THEN tenant
        WHEN 'day'    THEN day::text
        ELSE harness
    END::text
HAVING SUM(cost_usd) > 0
ORDER BY SUM(cost_usd) DESC
LIMIT sqlc.arg('lim') OFFSET sqlc.arg('off');

-- name: CountSpendGroups :one
-- Returns the total count of non-zero-cost groups matching the given filters,
-- without applying LIMIT/OFFSET. Used when the main query returns zero rows
-- (offset past end) so Total is still accurate.
WITH latest_per_session AS (
    SELECT DISTINCT ON (harness, session_id)
        harness,
        COALESCE(project_id::text, '') AS project_key,
        tenant,
        ts::date                       AS day,
        cost_usd,
        (payload->'telemetry'->>'turns')::int AS turns,
        external_ref
    FROM work_events
    WHERE cost_usd IS NOT NULL
      AND (sqlc.arg('tenant')::text = '' OR tenant = sqlc.arg('tenant')::text)
      AND (sqlc.arg('external_ref')::text = '' OR external_ref = sqlc.arg('external_ref')::text)
    ORDER BY harness, session_id, received_at DESC
),
spend_source AS (
    SELECT
        harness,
        project_key,
        tenant,
        day,
        cost_usd,
        turns
    FROM latest_per_session
)
SELECT COUNT(*)::bigint AS total_groups
FROM (
    SELECT 1
    FROM spend_source
    GROUP BY
        CASE sqlc.arg('group_by')
            WHEN 'agent'  THEN harness
            WHEN 'project' THEN project_key
            WHEN 'tenant' THEN tenant
            WHEN 'day'    THEN day::text
            ELSE harness
        END::text
    HAVING SUM(cost_usd) > 0
) AS groups;
