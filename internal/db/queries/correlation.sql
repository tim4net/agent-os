-- WP-B correlation engine. Groups work_events into work_units by the correlation key.
-- Per contract §7 the key is (project_id, external_ref, branch, sha); we ALSO group by
-- `tenant` so two tenants emitting the same external_ref with a NULL project_id are never
-- merged into one unit (ADR-002 D6 — employer tenants never co-mingle).
-- Events sharing no code/tracker anchor are surfaced as `correlated=false` units, grouped
-- by their (tenant, project) context — NEVER dropped (ADR-001 F3). The no-drop invariant:
-- SUM(event_count) over all units == COUNT(*) of work_events.

-- name: ListWorkUnits :many
SELECT
    COALESCE(tenant, '')                  AS tenant,
    COALESCE(project_id::text, '')::text  AS project_key,
    COALESCE(external_ref, '')            AS external_ref,
    COALESCE(branch, '')                  AS branch,
    COALESCE(sha, '')                     AS sha,
    COUNT(*)                              AS event_count,
    COUNT(DISTINCT (harness || ':' || session_id)) AS session_count,
    MIN(received_at)::timestamptz         AS first_event_at,
    MAX(received_at)::timestamptz         AS last_event_at,
    (external_ref IS NOT NULL OR branch IS NOT NULL OR sha IS NOT NULL) AS correlated,
    -- Latest status drives liveness honestly (F10): the status of the most recent
    -- event in the group, ignoring rows that carry no status (e.g. artifact.created).
    -- COALESCE to '' so a group of only status-less events scans cleanly (not NULL).
    COALESCE((array_agg(status ORDER BY received_at DESC) FILTER (WHERE status IS NOT NULL))[1], '')::text AS latest_status,
    -- Latest event kind (for the drill-down / uncorrelated label).
    COALESCE((array_agg(kind ORDER BY received_at DESC))[1], '')::text AS latest_kind,
    -- Most recent non-empty title.
    COALESCE((array_agg(title ORDER BY received_at DESC) FILTER (WHERE title IS NOT NULL AND title <> ''))[1], '')::text AS title,
    -- Distinct harnesses that contributed (for the harness pills).
    (array_agg(DISTINCT harness))::text[] AS harnesses,
    -- cost_usd is cumulative per session (contract §6); MAX is exact for a single-session
    -- unit. Multi-session units may undercount — acceptable for v1 display. NULL when no
    -- event carried a cost; stays nullable so the UI can omit it rather than show $0.00.
    (MAX(cost_usd))::numeric AS cost_usd
FROM work_events
GROUP BY tenant, project_id, external_ref, branch, sha
ORDER BY MAX(received_at) DESC,
         COALESCE(tenant,''), COALESCE(project_id::text,''),
         COALESCE(external_ref,''), COALESCE(branch,''), COALESCE(sha,'')
LIMIT $1 OFFSET $2;

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
-- Consistent with ListWorkUnits grouping (same key) so pagination Total matches.
SELECT COUNT(*) FROM (
    SELECT 1 FROM work_events
    GROUP BY tenant, project_id, external_ref, branch, sha
) g;
