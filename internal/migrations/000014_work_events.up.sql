-- Migration 014: work_events — the unit of record for the observability plane.
-- Realizes docs/work-event-contract.md v1.1 §6. See ADR-001.

-- Projects: the unit that groups work and declares its tracker source.
-- (Created here in 014 so work_events can FK to it; tracker columns added in 015.)
CREATE TABLE projects (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug        TEXT NOT NULL UNIQUE,           -- e.g. "agent-os", matched from project_hint/cwd
    name        TEXT NOT NULL,
    tenant      TEXT NOT NULL DEFAULT 'personal',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- work_events: append-only stream of uniform events emitted by every harness.
CREATE TABLE work_events (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id       UUID NOT NULL UNIQUE,         -- client-supplied idempotency key (contract §1)
    schema_version TEXT NOT NULL,                -- from body.schema, e.g. "agentos.work_event/v1"
    harness        TEXT NOT NULL,                -- hermes|claude|antigravity|codex|generic
    session_id     TEXT NOT NULL,                -- stable per agent session
    host           TEXT NOT NULL,                -- hostname (liveness keying, contract §4)
    pid            INTEGER,                       -- required when liveness_mode=supervised
    kind           TEXT NOT NULL,                -- session.start|heartbeat|end|artifact.created|server.started|server.stopped|note
    status         TEXT,                          -- conditional per contract §1
    liveness_mode  TEXT,                          -- supervised|bounded (set on session.start)
    project_id     UUID REFERENCES projects(id) ON DELETE SET NULL,  -- resolved at ingest
    tenant         TEXT NOT NULL DEFAULT 'personal',  -- validated against ingest key
    external_ref   TEXT,                          -- "SC-<n>" | "#<n>"
    branch         TEXT,
    sha            TEXT,
    cwd            TEXT,
    title          TEXT,
    cost_usd       NUMERIC,                       -- cumulative for the session; single source of truth
    payload        JSONB NOT NULL DEFAULT '{}',   -- free-form extras incl. telemetry; never core-interpreted
    ts             TIMESTAMPTZ NOT NULL,          -- emitter clock; display/order ONLY
    received_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()  -- server clock; the ONLY liveness clock
);

-- Liveness derivation reads events per (harness, session_id) ordered by received_at.
CREATE INDEX idx_work_events_session ON work_events (harness, session_id, received_at);
-- Correlation (WP-B) joins on these.
CREATE INDEX idx_work_events_correlation ON work_events (project_id, external_ref, branch, sha);
-- Timeline / recency scans.
CREATE INDEX idx_work_events_received_at ON work_events (received_at DESC);
