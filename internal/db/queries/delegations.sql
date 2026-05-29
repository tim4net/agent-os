-- name: CreateDelegation :one
INSERT INTO delegations (parent_agent_id, child_agent_name, task_goal, status, result_summary, metadata)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateDelegation :one
UPDATE delegations SET status = $2, result_summary = COALESCE($3, result_summary), completed_at = CASE WHEN $2 IN ('completed','failed','interrupted') THEN now() ELSE completed_at END, metadata = COALESCE($4, metadata)
WHERE id = $1
RETURNING *;

-- name: GetDelegation :one
SELECT * FROM delegations WHERE id = $1;

-- name: ListDelegations :many
SELECT * FROM delegations
WHERE ($1::uuid IS NULL OR parent_agent_id = $1)
  AND ($2::text = '' OR status = $2)
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: CountDelegations :one
SELECT COUNT(*) FROM delegations
WHERE ($1::uuid IS NULL OR parent_agent_id = $1)
  AND ($2::text = '' OR status = $2);

-- name: CleanStaleDelegations :exec
UPDATE delegations SET status = 'interrupted', completed_at = now(), result_summary = 'Stale delegation — auto-cleaned (no completion webhook received within timeout).'
WHERE status = 'running' AND created_at < now() - INTERVAL '30 minutes';
