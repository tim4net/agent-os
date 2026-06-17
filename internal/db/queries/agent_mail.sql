-- agent_mail.sql — agent-to-agent messaging (WP-101, issue #112).
-- All inbox reads are scoped by recipient_id (an agent only ever sees its own
-- mail). Expired mail (expires_at in the past) is excluded from inbox listings
-- and unread counts so the absence is observable without a background sweeper.

-- name: SendMail :one
INSERT INTO agent_mail (
    sender_id, recipient_id, subject, body, priority, status,
    reply_to_id, metadata, content_type, expires_at
)
VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10
)
RETURNING *;

-- name: GetMail :one
SELECT * FROM agent_mail
WHERE id = $1
  AND recipient_id = $2;

-- name: GetMailbox :many
SELECT * FROM agent_mail
WHERE recipient_id = sqlc.arg(recipient_id)
  AND status <> 'expired'
  AND (sqlc.narg(status)::mail_status IS NULL OR status = sqlc.narg(status))
  AND (sqlc.narg(priority)::mail_priority IS NULL OR priority = sqlc.narg(priority))
  AND (sqlc.narg(since)::timestamptz IS NULL OR created_at > sqlc.narg(since))
  -- Exclude time-expired mail from the inbox regardless of status, so a
  -- message whose expires_at has passed never surfaces.
  AND (expires_at IS NULL OR expires_at > NOW())
ORDER BY priority DESC, created_at DESC
LIMIT sqlc.arg(max_rows) OFFSET sqlc.arg(skip_rows);

-- name: CountMailbox :one
SELECT COUNT(*) FROM agent_mail
WHERE recipient_id = sqlc.arg(recipient_id)
  AND status <> 'expired'
  AND (sqlc.narg(status)::mail_status IS NULL OR status = sqlc.narg(status))
  AND (sqlc.narg(priority)::mail_priority IS NULL OR priority = sqlc.narg(priority))
  AND (sqlc.narg(since)::timestamptz IS NULL OR created_at > sqlc.narg(since))
  AND (expires_at IS NULL OR expires_at > NOW());

-- name: CountUnread :one
SELECT COUNT(*) FROM agent_mail
WHERE recipient_id = sqlc.arg(recipient_id)
  AND status IN ('queued', 'delivered')
  AND (expires_at IS NULL OR expires_at > NOW());

-- name: MarkMailRead :one
UPDATE agent_mail
SET status = 'read', read_at = NOW()
WHERE id = sqlc.arg(id)
  AND recipient_id = sqlc.arg(recipient_id)
  AND status IN ('queued', 'delivered')
RETURNING *;

-- name: ExpireMail :exec
UPDATE agent_mail
SET status = 'expired'
WHERE status IN ('queued', 'delivered')
  AND expires_at IS NOT NULL
  AND expires_at <= NOW();

-- name: ExpireMailByID :one
UPDATE agent_mail
SET status = 'expired'
WHERE id = sqlc.arg(id)
  AND recipient_id = sqlc.arg(recipient_id)
RETURNING *;

-- name: GetMailThread :many
SELECT * FROM agent_mail
WHERE recipient_id = sqlc.arg(recipient_id)
  AND (id = sqlc.arg(root_id) OR reply_to_id = sqlc.arg(root_id))
ORDER BY created_at ASC;
