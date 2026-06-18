-- name: ListArtifacts :many
SELECT * FROM artifacts
WHERE owner_id = $1
  AND ($2::text IS NULL OR $2::text = '' OR type = $2)
  AND ($3::uuid IS NULL OR agent_id = $3)
ORDER BY created_at DESC
LIMIT $4 OFFSET $5;

-- name: GetArtifact :one
SELECT * FROM artifacts WHERE id = $1 AND owner_id = $2;

-- name: CreateArtifact :one
INSERT INTO artifacts (owner_id, agent_id, type, title, description, file_path, mime_type, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: DeleteArtifact :exec
DELETE FROM artifacts WHERE id = $1 AND owner_id = $2;

-- name: GetArtifactByPath :one
SELECT * FROM artifacts WHERE file_path = $1 AND owner_id = $2;

-- name: CountArtifacts :one
SELECT COUNT(*) FROM artifacts WHERE owner_id = $1 AND ($2::text IS NULL OR $2::text = '' OR type = $2);
