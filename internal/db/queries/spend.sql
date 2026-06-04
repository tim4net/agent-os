-- name: AggregateSpend :many
-- Aggregates USAGE (tokens_used + turns) and cost_usd from work_events, grouped by
-- the requested dimension (agent/harness, project, tenant, or day). Pure read over
-- existing work_events.
--
-- Provider-aware spend (Option A): token/turn usage is ALWAYS meaningful and is the
-- primary metric. cost_usd is OPTIONAL — subscription accounts never report it, so we
-- must NOT drop sessions that lack a dollar figure (the old `WHERE cost_usd IS NOT NULL`
-- + `HAVING SUM(cost_usd) > 0` did exactly that, hiding all subscription usage). cost_usd
-- is summed as a nullable value: NULL when no session in the group reported a cost.
--
-- Per contract §5, cost_usd / telemetry is cumulative for the session and "latest
-- received_at wins"; we dedupe to the latest event per (harness, session_id) before
-- rolling up. tokens_used and turns come from payload->telemetry (non-core fields).
-- Tenant-scoped: @tenant = '' returns all tenants; otherwise scopes to one (UNCHANGED —
-- this isolation predicate is load-bearing and must not regress).
WITH latest_per_session AS (
    SELECT DISTINCT ON (harness, session_id)
        harness,
        COALESCE(project_id::text, '') AS project_key,
        tenant,
        ts::date                       AS day,
        cost_usd,
        (payload->'telemetry'->>'turns')::int        AS turns,
        (payload->'telemetry'->>'tokens_used')::bigint AS tokens_used,
        external_ref
    FROM work_events
    WHERE (sqlc.arg('tenant')::text = '' OR tenant = sqlc.arg('tenant')::text)
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
        turns,
        tokens_used
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
    SUM(cost_usd)::numeric                                AS total_cost_usd,
    COALESCE(SUM(COALESCE(tokens_used, 0)), 0)::bigint    AS total_tokens,
    COALESCE(SUM(COALESCE(turns, 0)), 0)::bigint          AS total_turns,
    COUNT(*)::bigint                                      AS session_count,
    COUNT(*) OVER()::bigint                               AS total_groups
FROM spend_source
GROUP BY
    CASE sqlc.arg('group_by')
        WHEN 'agent'  THEN harness
        WHEN 'project' THEN project_key
        WHEN 'tenant' THEN tenant
        WHEN 'day'    THEN day::text
        ELSE harness
    END::text
-- Include any group with real activity: tokens OR turns OR a dollar cost. This keeps
-- subscription agents (cost NULL, tokens > 0) visible while still excluding empty noise.
HAVING COALESCE(SUM(COALESCE(tokens_used, 0)), 0) > 0
    OR COALESCE(SUM(COALESCE(turns, 0)), 0) > 0
    OR COALESCE(SUM(cost_usd), 0) > 0
-- Usage-first ordering: most tokens, then turns, then dollars (nulls last).
ORDER BY
    COALESCE(SUM(COALESCE(tokens_used, 0)), 0) DESC,
    COALESCE(SUM(COALESCE(turns, 0)), 0) DESC,
    SUM(cost_usd) DESC NULLS LAST
LIMIT sqlc.arg('lim') OFFSET sqlc.arg('off');

-- name: CountSpendGroups :one
-- Returns the total count of active groups matching the given filters, without
-- LIMIT/OFFSET. "Active" mirrors AggregateSpend's HAVING (tokens OR turns OR cost),
-- so the count stays consistent with the rows actually returned.
WITH latest_per_session AS (
    SELECT DISTINCT ON (harness, session_id)
        harness,
        COALESCE(project_id::text, '') AS project_key,
        tenant,
        ts::date                       AS day,
        cost_usd,
        (payload->'telemetry'->>'turns')::int        AS turns,
        (payload->'telemetry'->>'tokens_used')::bigint AS tokens_used,
        external_ref
    FROM work_events
    WHERE (sqlc.arg('tenant')::text = '' OR tenant = sqlc.arg('tenant')::text)
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
        turns,
        tokens_used
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
    HAVING COALESCE(SUM(COALESCE(tokens_used, 0)), 0) > 0
        OR COALESCE(SUM(COALESCE(turns, 0)), 0) > 0
        OR COALESCE(SUM(cost_usd), 0) > 0
) AS groups;
