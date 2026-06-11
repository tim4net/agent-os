-- name: ListConversations :many
SELECT c.* FROM conversations c
JOIN agents a ON c.agent_id = a.id
WHERE a.visible = true
AND c.owner_id = $1
AND ($2::uuid IS NULL OR c.agent_id = $2)
ORDER BY c.updated_at DESC;

-- name: GetConversation :one
SELECT * FROM conversations WHERE id = $1 AND owner_id = $2;

-- name: CreateConversation :one
INSERT INTO conversations (owner_id, agent_id, title, metadata)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: DeleteConversation :exec
DELETE FROM conversations WHERE id = $1 AND owner_id = $2;

-- name: UpdateConversation :one
UPDATE conversations SET title = $2, updated_at = NOW()
WHERE id = $1 AND owner_id = $3
RETURNING *;

-- name: UpdateConversationMetadata :one
UPDATE conversations SET metadata = $2, updated_at = NOW()
WHERE id = $1 AND owner_id = $3
RETURNING *;

-- name: UpdateConversationSummary :one
UPDATE conversations SET summary = $2, updated_at = NOW()
WHERE id = $1 AND owner_id = $3
RETURNING *;

-- name: ListMessages :many
SELECT m.* FROM messages m
JOIN conversations c ON m.conversation_id = c.id
WHERE m.conversation_id = $1 AND c.owner_id = $2
ORDER BY m.created_at ASC;

-- name: CreateMessage :one
INSERT INTO messages (owner_id, conversation_id, role, content, metadata)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: DeleteMessagesByConversation :execrows
DELETE FROM messages WHERE conversation_id = $1 AND owner_id = $2;

-- name: DeleteLastExchange :execrows
WITH last_user AS (
    SELECT id FROM messages
    WHERE messages.conversation_id = $1 AND messages.owner_id = $2 AND messages.role = 'user'
    ORDER BY messages.created_at DESC LIMIT 1
),
last_assistant AS (
    SELECT id FROM messages
    WHERE messages.conversation_id = $1 AND messages.owner_id = $2 AND messages.role = 'assistant'
    ORDER BY messages.created_at DESC LIMIT 1
)
DELETE FROM messages WHERE id IN (SELECT id FROM last_user) OR id IN (SELECT id FROM last_assistant);

-- name: GetLastUserMessage :one
SELECT m.* FROM messages m
JOIN conversations c ON m.conversation_id = c.id
WHERE m.conversation_id = $1 AND c.owner_id = $2 AND m.role = 'user'
ORDER BY m.created_at DESC LIMIT 1;

-- name: DeleteMessage :exec
DELETE FROM messages WHERE id = $1 AND owner_id = $2;
