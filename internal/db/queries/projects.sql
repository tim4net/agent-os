-- name: GetProject :one
SELECT * FROM projects WHERE id = $1 AND owner_id = $2;

-- name: GetProjectBySlug :one
SELECT * FROM projects WHERE slug = $1 AND owner_id = $2;

-- name: ListProjects :many
SELECT * FROM projects WHERE owner_id = $1 ORDER BY name;

-- name: CreateProject :one
INSERT INTO projects (owner_id, slug, name, tenant, tracker, external_ref, repo_url)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: EnsureProjectBySlug :one
-- Idempotent resolution used by the ingest project resolver (contract §1).
INSERT INTO projects (owner_id, slug, name, tenant)
VALUES ($1, $2, $3, $4)
ON CONFLICT (slug) DO UPDATE SET updated_at = NOW()
RETURNING *;

-- name: UpdateProject :one
-- Updates all mutable project fields: name, tracker, external_ref, repo_url.
-- (Renamed from UpdateProjectTracker once name became mutable so the query
-- name reflects what it actually persists — previously name was silently
-- dropped because it wasn't in the SET clause.)
UPDATE projects SET name = $2, tracker = $3, external_ref = $4, repo_url = $5, updated_at = NOW()
WHERE id = $1 AND owner_id = $6
RETURNING *;
