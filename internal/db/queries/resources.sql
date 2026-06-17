-- name: GetResource :one
SELECT id, slug, kind, label, provider, is_secret, enc_value, enc_config, config, last4, status, created_at, updated_at, owner_id, enc_key_version
FROM resources WHERE id = $1;

-- name: GetResourceBySlug :one
SELECT id, slug, kind, label, provider, is_secret, enc_value, enc_config, config, last4, status, created_at, updated_at, owner_id, enc_key_version
FROM resources WHERE slug = $1;

-- name: ListResources :many
SELECT id, slug, kind, label, provider, is_secret, enc_value, enc_config, config, last4, status, created_at, updated_at, owner_id, enc_key_version
FROM resources ORDER BY kind, label, slug;

-- name: ListResourcesByKind :many
SELECT id, slug, kind, label, provider, is_secret, enc_value, enc_config, config, last4, status, created_at, updated_at, owner_id, enc_key_version
FROM resources WHERE kind = $1 ORDER BY label, slug;

-- name: CreateResource :one
INSERT INTO resources (slug, kind, label, provider, is_secret, enc_value, enc_config, config, last4, status, enc_key_version)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING id, slug, kind, label, provider, is_secret, enc_value, enc_config, config, last4, status, created_at, updated_at, owner_id, enc_key_version;

-- name: UpdateResource :one
UPDATE resources
SET label = $2, provider = $3, is_secret = $4, enc_value = $5, enc_config = $6, config = $7, last4 = $8, status = $9, enc_key_version = $10, updated_at = NOW()
WHERE id = $1
RETURNING id, slug, kind, label, provider, is_secret, enc_value, enc_config, config, last4, status, created_at, updated_at, owner_id, enc_key_version;

-- name: DeleteResource :exec
DELETE FROM resources WHERE id = $1;

-- name: GrantResource :one
INSERT INTO agent_grants (agent_id, resource_id, scope)
VALUES ($1, $2, $3)
ON CONFLICT (agent_id, resource_id, scope) DO UPDATE SET granted_at = agent_grants.granted_at
RETURNING id, agent_id, resource_id, scope, granted_at, owner_id;

-- name: RevokeResource :exec
DELETE FROM agent_grants WHERE agent_id = $1 AND resource_id = $2;

-- name: ListGrantsForAgent :many
SELECT g.id, g.agent_id, g.resource_id, g.scope, g.granted_at, g.owner_id
FROM agent_grants g WHERE g.agent_id = $1;

-- name: ListGrantsForResource :many
SELECT g.id, g.agent_id, g.resource_id, g.scope, g.granted_at, g.owner_id
FROM agent_grants g WHERE g.resource_id = $1;

-- name: ListAllGrants :many
SELECT id, agent_id, resource_id, scope, granted_at, owner_id FROM agent_grants;

-- name: ListResourcesForAgent :many
SELECT r.id, r.slug, r.kind, r.label, r.provider, r.is_secret, r.enc_value, r.enc_config, r.config, r.last4, r.status, r.created_at, r.updated_at, r.owner_id, r.enc_key_version
FROM resources r
JOIN agent_grants g ON g.resource_id = r.id
WHERE g.agent_id = $1
ORDER BY r.kind, r.label;
