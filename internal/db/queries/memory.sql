-- name: ListMemoryIndex :many
SELECT * FROM memory_index ORDER BY last_indexed DESC;

-- name: GetMemoryByPath :one
SELECT * FROM memory_index WHERE file_path = $1;

-- name: UpsertMemory :one
INSERT INTO memory_index (file_path, title, content, tags)
VALUES ($1, $2, $3, $4)
ON CONFLICT (file_path) DO UPDATE SET
    title = EXCLUDED.title,
    content = EXCLUDED.content,
    tags = EXCLUDED.tags,
    last_indexed = NOW()
RETURNING *;

-- name: SearchMemory :many
SELECT * FROM memory_index
WHERE to_tsvector('english', coalesce(content, '')) @@ websearch_to_tsquery('english', $1)
ORDER BY ts_rank(to_tsvector('english', coalesce(content, '')), websearch_to_tsquery('english', $1)) DESC
LIMIT $2;

-- name: DeleteMemory :exec
DELETE FROM memory_index WHERE file_path = $1;
