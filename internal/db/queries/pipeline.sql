-- name: ListPipelineItems :many
SELECT * FROM pipeline_items
WHERE ($1::text IS NULL OR status = $1)
  AND ($2::text IS NULL OR type = $2)
ORDER BY created_at DESC;

-- name: GetPipelineItem :one
SELECT * FROM pipeline_items WHERE id = $1;

-- name: CreatePipelineItem :one
INSERT INTO pipeline_items (type, title, status, content, metadata, agent_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdatePipelineItem :one
UPDATE pipeline_items SET title = $2, status = $3, content = $4, metadata = $5, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: DeletePipelineItem :exec
DELETE FROM pipeline_items WHERE id = $1;
