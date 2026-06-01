-- name: AggregateSpend :many
-- Aggregates cost_usd + num_turns (from telemetry) from work_events, grouped by the
-- requested dimension (agent/harness, project, tenant, or day). Pure read over
-- existing work_events — no migration needed (WP-K).
-- Cost has ONE source of truth: top-level cost_usd (contract §5).
-- Turns are counted from payload->telemetry->turns (non-null only).
-- Tenant-scoped: @tenant = '' returns all tenants; otherwise scopes to one.
WITH spend_source AS (
    SELECT
        harness,
        COALESCE(project_id::text, '') AS project_key,
        tenant,
        ts::date                       AS day,
        cost_usd,
        (payload->'telemetry'->>'turns')::int AS turns
    FROM work_events
    WHERE cost_usd IS NOT NULL
      AND (sqlc.arg('tenant')::text = '' OR tenant = sqlc.arg('tenant')::text)
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
    COALESCE(SUM(COALESCE(turns, 0))::bigint, 0)::bigint AS total_turns
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
