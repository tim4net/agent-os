-- name: ListMemoryIndex :many
SELECT * FROM memory_index WHERE owner_id = $1 ORDER BY last_indexed DESC;

-- name: GetMemoryByPath :one
SELECT * FROM memory_index WHERE file_path = $1 AND owner_id = $2;

-- name: UpsertMemory :one
INSERT INTO memory_index (owner_id, file_path, title, content, tags, project_id)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (file_path) DO UPDATE SET
    title = EXCLUDED.title,
    content = EXCLUDED.content,
    tags = EXCLUDED.tags,
    project_id = EXCLUDED.project_id,
    last_indexed = NOW()
RETURNING *;

-- name: SearchMemory :many
SELECT * FROM memory_index
WHERE owner_id = $1
  AND to_tsvector('english', coalesce(content, '')) @@ websearch_to_tsquery('english', $2)
  AND (project_id = $4 OR $4 IS NULL)
ORDER BY ts_rank(to_tsvector('english', coalesce(content, '')), websearch_to_tsquery('english', $2)) DESC
LIMIT $3;

-- name: DeleteMemory :exec
DELETE FROM memory_index WHERE file_path = $1 AND owner_id = $2;

-- name: CountMemoryByProject :one
-- Workspace surface (issue #134): how many notes belong to a project.
SELECT COUNT(*) FROM memory_index
WHERE owner_id = $1 AND (project_id = $2 OR $2 IS NULL);
