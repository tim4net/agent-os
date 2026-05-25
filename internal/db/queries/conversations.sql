-- name: ListConversations :many
SELECT * FROM conversations
WHERE ($1::uuid IS NULL OR agent_id = $1)
ORDER BY updated_at DESC;

-- name: GetConversation :one
SELECT * FROM conversations WHERE id = $1;

-- name: CreateConversation :one
INSERT INTO conversations (agent_id, title, metadata)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpdateConversation :one
UPDATE conversations SET title = $2, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: ListMessages :many
SELECT * FROM messages
WHERE conversation_id = $1
ORDER BY created_at ASC;

-- name: CreateMessage :one
INSERT INTO messages (conversation_id, role, content, metadata)
VALUES ($1, $2, $3, $4)
RETURNING *;
