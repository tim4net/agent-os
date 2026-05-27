-- name: ListSkills :many
SELECT * FROM skills
ORDER BY created_at DESC;

-- name: ListSkillSummaries :many
SELECT id, name, description, category, triggers, agent_id, created_at, updated_at
FROM skills
ORDER BY created_at DESC;

-- name: GetSkill :one
SELECT * FROM skills WHERE id = $1;

-- name: CreateSkill :one
INSERT INTO skills (name, description, category, content, triggers, agent_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateSkill :one
UPDATE skills SET name = $2, description = $3, category = $4, content = $5, triggers = $6, agent_id = $7, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteSkill :exec
DELETE FROM skills WHERE id = $1;

-- name: UpsertSkill :one
INSERT INTO skills (name, description, category, content, triggers, agent_id)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (name) DO UPDATE SET
    description = EXCLUDED.description,
    category = EXCLUDED.category,
    content = EXCLUDED.content,
    triggers = EXCLUDED.triggers,
    updated_at = NOW()
RETURNING *;

-- name: ListSkillsByAgent :many
SELECT * FROM skills
WHERE agent_id = $1
ORDER BY created_at DESC;
