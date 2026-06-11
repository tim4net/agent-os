-- name: ListPipelineItems :many
SELECT * FROM pipeline_items
WHERE owner_id = $1
  AND ($2::text = '' OR status = $2)
  AND ($3::text = '' OR type = $3)
ORDER BY created_at DESC;

-- name: GetPipelineItem :one
SELECT * FROM pipeline_items WHERE id = $1 AND owner_id = $2;

-- name: CreatePipelineItem :one
INSERT INTO pipeline_items (owner_id, type, title, status, content, metadata, agent_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdatePipelineItem :one
UPDATE pipeline_items SET title = $2, status = $3, content = $4, metadata = $5, updated_at = NOW()
WHERE id = $1 AND owner_id = $6
RETURNING *;

-- name: DeletePipelineItem :exec
DELETE FROM pipeline_items WHERE id = $1 AND owner_id = $2;
