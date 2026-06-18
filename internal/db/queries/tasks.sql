-- name: ListTasks :many
SELECT * FROM tasks
WHERE owner_id = $1
  AND ($2::text = '' OR status = $2)
  AND ($3::uuid IS NULL OR agent_id = $3)
ORDER BY priority DESC, created_at ASC;

-- name: GetTask :one
SELECT * FROM tasks WHERE id = $1 AND owner_id = $2;

-- name: CreateTask :one
INSERT INTO tasks (owner_id, agent_id, title, description, status, priority, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateTask :one
UPDATE tasks SET title = $2, description = $3, status = $4, priority = $5, metadata = $6, updated_at = NOW()
WHERE id = $1 AND owner_id = $7
RETURNING *;

-- name: DeleteTask :exec
DELETE FROM tasks WHERE id = $1 AND owner_id = $2;

-- name: CreateSubtask :one
INSERT INTO tasks (owner_id, agent_id, title, description, status, priority, metadata, parent_task_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListSubtasks :many
SELECT * FROM tasks WHERE parent_task_id = $1 AND owner_id = $2
ORDER BY priority DESC, created_at ASC;

-- name: CountSubtasks :one
SELECT COUNT(*) FROM tasks WHERE parent_task_id = $1 AND owner_id = $2;
