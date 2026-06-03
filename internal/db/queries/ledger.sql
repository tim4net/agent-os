-- name: AppendRunLog :one
INSERT INTO run_log (event_type, pr_ref, wp_ref, summary, payload) VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: AppendFinding :one
INSERT INTO findings (pr_ref, wp_ref, gate, author_agent, model, severity, class, root_cause, summary) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING *;

-- name: ListRunLog :many
SELECT * FROM run_log ORDER BY ts DESC LIMIT $1 OFFSET $2;

-- name: ListFindings :many
SELECT * FROM findings ORDER BY ts DESC LIMIT $1 OFFSET $2;

-- name: ListFindingsByClass :many
SELECT * FROM findings WHERE class = $1 ORDER BY ts DESC LIMIT $2 OFFSET $3;

-- name: ListFindingsBySeverity :many
SELECT * FROM findings WHERE severity = $1 ORDER BY ts DESC LIMIT $2 OFFSET $3;

-- name: ListFindingsByWpRef :many
SELECT * FROM findings WHERE wp_ref = $1 ORDER BY ts DESC LIMIT $2 OFFSET $3;

-- name: ListRunLogByWpRef :many
SELECT * FROM run_log WHERE wp_ref = $1 ORDER BY ts DESC LIMIT $2 OFFSET $3;

-- name: CountRunLogByWpRef :one
SELECT COUNT(*)::bigint FROM run_log WHERE wp_ref = $1;

-- name: CountRunLog :one
SELECT COUNT(*)::bigint FROM run_log;

-- name: CountFindings :one
SELECT COUNT(*)::bigint FROM findings;

-- name: CountFindingsByClass :one
SELECT COUNT(*)::bigint FROM findings WHERE class = $1;

-- name: CountFindingsBySeverity :one
SELECT COUNT(*)::bigint FROM findings WHERE severity = $1;

-- name: CountFindingsByWpRef :one
SELECT COUNT(*)::bigint FROM findings WHERE wp_ref = $1;

-- name: RecurringFindings :many
SELECT class, author_agent, wp_ref, COUNT(*)::bigint AS count
FROM findings
GROUP BY class, author_agent, wp_ref
HAVING COUNT(*) >= $1
ORDER BY count DESC;
