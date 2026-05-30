-- name: GetProject :one
SELECT * FROM projects WHERE id = $1;

-- name: GetProjectBySlug :one
SELECT * FROM projects WHERE slug = $1;

-- name: ListProjects :many
SELECT * FROM projects ORDER BY name;

-- name: CreateProject :one
INSERT INTO projects (slug, name, tenant, tracker, external_ref, repo_url)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: EnsureProjectBySlug :one
-- Idempotent resolution used by the ingest project resolver (contract §1).
INSERT INTO projects (slug, name, tenant)
VALUES ($1, $2, $3)
ON CONFLICT (slug) DO UPDATE SET updated_at = NOW()
RETURNING *;

-- name: UpdateProjectTracker :one
UPDATE projects SET tracker = $2, external_ref = $3, repo_url = $4, updated_at = NOW()
WHERE id = $1
RETURNING *;
