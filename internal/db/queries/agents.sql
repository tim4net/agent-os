-- name: ListAgents :many
SELECT * FROM agents ORDER BY created_at;

-- name: ListVisibleAgents :many
SELECT * FROM agents WHERE visible = true ORDER BY created_at;

-- name: GetAgent :one
SELECT * FROM agents WHERE id = $1;

-- name: GetAgentByName :one
SELECT * FROM agents WHERE name = $1;

-- name: CreateAgent :one
INSERT INTO agents (name, display_name, harness, base_url, metadata)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateAgentStatus :one
UPDATE agents SET status = $2, last_seen = NOW(), updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateAgent :one
UPDATE agents SET display_name = $2, harness = $3, base_url = $4, metadata = $5, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateAgentConfig :one
UPDATE agents SET role = $2, system_prompt = $3, persona = $4, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: EnsureAgent :one
INSERT INTO agents (name, display_name, harness, base_url, metadata)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (name) DO NOTHING
RETURNING *;

-- name: RenameAgent :one
UPDATE agents SET name = $2, display_name = $3, updated_at = NOW()
WHERE id = $1 AND owner_id = $4
RETURNING *;

-- name: GetAgentByNameAndOwner :one
SELECT * FROM agents WHERE name = $1 AND owner_id = $2;

-- name: DeleteAgent :exec
DELETE FROM agents WHERE id = $1;
