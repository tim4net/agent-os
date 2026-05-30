-- WP-B correlation engine. Groups work_events into work_units by the correlation key
-- (project_id, external_ref, branch, sha) per contract §7. Events lacking any key part
-- are surfaced as 'uncorrelated' — NEVER dropped (ADR-001 F3).
-- Tracker enrichment (LEFT JOIN tracker_items) is added when WP-E's table lands; this
-- query stands alone on work_events so WP-B does not block on WP-E.

-- name: ListWorkUnits :many
-- One row per correlation group, newest activity first. The grouping coalesces NULL key
-- parts to '' so a stable group identity exists; `correlated` is true when the group
-- carries at least one real key part.
SELECT
    COALESCE(project_id::text, '')::text  AS project_key,
    COALESCE(external_ref, '')            AS external_ref,
    COALESCE(branch, '')                  AS branch,
    COALESCE(sha, '')                     AS sha,
    COUNT(*)                              AS event_count,
    COUNT(DISTINCT session_id)            AS session_count,
    MIN(received_at)::timestamptz         AS first_event_at,
    MAX(received_at)::timestamptz         AS last_event_at,
    (external_ref IS NOT NULL OR branch IS NOT NULL OR sha IS NOT NULL) AS correlated
FROM work_events
GROUP BY project_id, external_ref, branch, sha,
         (external_ref IS NOT NULL OR branch IS NOT NULL OR sha IS NOT NULL)
ORDER BY MAX(received_at) DESC
LIMIT $1 OFFSET $2;

-- name: GetWorkUnitEvents :many
-- All events belonging to one correlation group (for drill-down). NULL-safe equality so
-- the uncorrelated group ('','','') matches rows with NULL key parts.
SELECT *
FROM work_events
WHERE COALESCE(project_id::text, '') = $1
  AND COALESCE(external_ref, '')     = $2
  AND COALESCE(branch, '')           = $3
  AND COALESCE(sha, '')              = $4
ORDER BY received_at ASC;

-- name: CountWorkUnits :one
SELECT COUNT(*) FROM (
    SELECT 1 FROM work_events
    GROUP BY project_id, external_ref, branch, sha,
             (external_ref IS NOT NULL OR branch IS NOT NULL OR sha IS NOT NULL)
) g;
