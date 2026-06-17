-- Migration 028: agent-to-agent messaging (WP-101, issue #112).
--
-- Asynchronous agent-to-agent messaging with persistent delivery, priority
-- queues, read tracking, and reply threading. This extends the existing
-- synchronous delegation path (POST /agents/{id}/delegate → delegations table)
-- into a full mailbox layer.
--
-- All inbox queries are scoped by recipient_id so an agent can only ever read
-- its own mail. Self-mail is rejected at the API layer (sender != recipient).

CREATE TYPE mail_priority AS ENUM ('low', 'normal', 'high', 'urgent');
CREATE TYPE mail_status AS ENUM ('queued', 'delivered', 'read', 'expired');

CREATE TABLE agent_mail (
    id           BIGSERIAL    PRIMARY KEY,
    sender_id    UUID         NOT NULL REFERENCES agents(id),
    recipient_id UUID         NOT NULL REFERENCES agents(id),
    subject      TEXT         NOT NULL DEFAULT '',
    body         TEXT         NOT NULL,
    priority     mail_priority NOT NULL DEFAULT 'normal',
    status       mail_status  NOT NULL DEFAULT 'queued',
    reply_to_id BIGINT        REFERENCES agent_mail(id),
    metadata     JSONB        NOT NULL DEFAULT '{}',
    content_type TEXT         NOT NULL DEFAULT 'notification',
    expires_at   TIMESTAMPTZ,
    read_at      TIMESTAMPTZ,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Inbox lookups: filter by recipient + status, newest/priority first.
CREATE INDEX idx_mail_recipient_status   ON agent_mail(recipient_id, status);
CREATE INDEX idx_mail_recipient_created  ON agent_mail(recipient_id, created_at DESC);
-- Priority queue ordering (recipient + priority for "what's most urgent").
CREATE INDEX idx_mail_priority           ON agent_mail(recipient_id, priority);
-- Reply-thread traversal.
CREATE INDEX idx_mail_reply_to           ON agent_mail(reply_to_id);
