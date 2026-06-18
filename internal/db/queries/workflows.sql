-- name: ListWorkflows :many
SELECT * FROM workflows
WHERE owner_id = $1
ORDER BY created_at DESC;

-- name: GetWorkflow :one
SELECT * FROM workflows WHERE id = $1 AND owner_id = $2;

-- name: CreateWorkflow :one
INSERT INTO workflows (owner_id, name, description, steps, agent_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateWorkflow :one
UPDATE workflows SET name = $2, description = $3, steps = $4, agent_id = $5, updated_at = NOW()
WHERE id = $1 AND owner_id = $6
RETURNING *;

-- name: DeleteWorkflow :exec
DELETE FROM workflows WHERE id = $1 AND owner_id = $2;

-- name: CreateWorkflowRun :one
INSERT INTO workflow_runs (owner_id, workflow_id, status, current_step, result)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateWorkflowRun :one
UPDATE workflow_runs SET status = $2, current_step = $3, result = $4, updated_at = NOW()
WHERE id = $1 AND owner_id = $5
RETURNING *;

-- name: GetWorkflowRun :one
SELECT * FROM workflow_runs WHERE id = $1 AND owner_id = $2;

-- name: ListWorkflowRuns :many
SELECT * FROM workflow_runs
WHERE workflow_id = $1 AND owner_id = $2
ORDER BY created_at DESC;

-- name: GetLatestWorkflowRun :one
SELECT * FROM workflow_runs
WHERE workflow_id = $1 AND owner_id = $2
ORDER BY created_at DESC
LIMIT 1;
