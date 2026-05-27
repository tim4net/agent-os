-- name: ListTasks :many
SELECT * FROM tasks
WHERE ($1::text = '' OR status = $1)
  AND ($2::uuid IS NULL OR agent_id = $2)
ORDER BY priority DESC, created_at ASC;

-- name: GetTask :one
SELECT * FROM tasks WHERE id = $1;

-- name: CreateTask :one
INSERT INTO tasks (agent_id, title, description, status, priority, metadata)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateTask :one
UPDATE tasks SET title = $2, description = $3, status = $4, priority = $5, metadata = $6, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteTask :exec
DELETE FROM tasks WHERE id = $1;

-- name: CreateSubtask :one
INSERT INTO tasks (agent_id, title, description, status, priority, metadata, parent_task_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListSubtasks :many
SELECT * FROM tasks WHERE parent_task_id = $1
ORDER BY priority DESC, created_at ASC;

-- name: CountSubtasks :one
SELECT COUNT(*) FROM tasks WHERE parent_task_id = $1;
