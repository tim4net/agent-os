-- name: ListGoals :many
SELECT * FROM goals WHERE owner_id = $1 ORDER BY created_at DESC;

-- name: GetGoal :one
SELECT * FROM goals WHERE id = $1 AND owner_id = $2;

-- name: CreateGoal :one
INSERT INTO goals (owner_id, title, description, status, progress, target_date, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateGoal :one
UPDATE goals SET title = $2, description = $3, status = $4, progress = $5, target_date = $6, metadata = $7, updated_at = NOW()
WHERE id = $1 AND owner_id = $8
RETURNING *;

-- name: DeleteGoal :exec
DELETE FROM goals WHERE id = $1 AND owner_id = $2;
