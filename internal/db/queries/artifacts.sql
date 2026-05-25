-- name: ListArtifacts :many
SELECT * FROM artifacts
WHERE ($1::text IS NULL OR type = $1)
  AND ($2::uuid IS NULL OR agent_id = $2)
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: GetArtifact :one
SELECT * FROM artifacts WHERE id = $1;

-- name: CreateArtifact :one
INSERT INTO artifacts (agent_id, type, title, description, file_path, mime_type, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: DeleteArtifact :exec
DELETE FROM artifacts WHERE id = $1;

-- name: CountArtifacts :one
SELECT COUNT(*) FROM artifacts WHERE ($1::text IS NULL OR type = $1);
