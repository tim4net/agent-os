-- name: ListConversations :many
SELECT c.* FROM conversations c
JOIN agents a ON c.agent_id = a.id
WHERE a.visible = true
AND ($1::uuid IS NULL OR c.agent_id = $1)
ORDER BY c.updated_at DESC;

-- name: GetConversation :one
SELECT * FROM conversations WHERE id = $1;

-- name: CreateConversation :one
INSERT INTO conversations (agent_id, title, metadata)
VALUES ($1, $2, $3)
RETURNING *;

-- name: DeleteConversation :exec
-- Deletes a conversation and (via ON DELETE CASCADE) its messages. Used to roll
-- back a freshly-created conversation when the very first chat turn fails before
-- streaming, so a failed send never leaves an orphan conversation behind.
DELETE FROM conversations WHERE id = $1;

-- name: UpdateConversation :one
UPDATE conversations SET title = $2, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateConversationMetadata :one
UPDATE conversations SET metadata = $2, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateConversationSummary :one
UPDATE conversations SET summary = $2, updated_at = NOW()
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

-- name: DeleteMessagesByConversation :execrows
DELETE FROM messages WHERE conversation_id = $1;

-- name: DeleteLastExchange :execrows
WITH last_user AS (
    SELECT id FROM messages
    WHERE messages.conversation_id = $1 AND messages.role = 'user'
    ORDER BY messages.created_at DESC LIMIT 1
),
last_assistant AS (
    SELECT id FROM messages
    WHERE messages.conversation_id = $1 AND messages.role = 'assistant'
    ORDER BY messages.created_at DESC LIMIT 1
)
DELETE FROM messages WHERE id IN (SELECT id FROM last_user) OR id IN (SELECT id FROM last_assistant);

-- name: GetLastUserMessage :one
SELECT * FROM messages
WHERE conversation_id = $1 AND role = 'user'
ORDER BY created_at DESC LIMIT 1;

-- name: DeleteMessage :exec
DELETE FROM messages WHERE id = $1;
