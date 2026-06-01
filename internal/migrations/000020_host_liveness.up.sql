-- WP-N: Host liveness feed for bounded-session crash detection.
-- Stores the latest process-liveness report per (host, pid) / cwd from
-- the host-reporter agent. The liveness derivation (contract §4) uses this
-- table to prove a bounded process is alive — absence of proof means stale.
-- Running status for bounded sessions requires positive proof from this table.

CREATE TABLE IF NOT EXISTS host_liveness (
    id          BIGSERIAL       PRIMARY KEY,
    host        TEXT            NOT NULL,                 -- hostname (matches work_event.host)
    pid         INT             NOT NULL,                 -- process PID
    session_id  TEXT            NOT NULL DEFAULT '',      -- optional: agent session_id if known
    harness     TEXT            NOT NULL DEFAULT '',       -- optional: harness name if known
    cwd         TEXT            NOT NULL DEFAULT '',       -- working directory of the process
    tenant      TEXT            NOT NULL DEFAULT 'personal',
    alive       BOOLEAN         NOT NULL DEFAULT TRUE,    -- TRUE = process still alive; FALSE = reporter says gone
    seen_at     TIMESTAMPTZ     NOT NULL DEFAULT NOW(),    -- server clock when report was received
    UNIQUE (host, pid)                              -- one row per (host, pid); upserted on each report
);

-- Index for lookups by host (used by worktree scanner to correlate).
CREATE INDEX IF NOT EXISTS idx_host_liveness_host ON host_liveness (host);

-- Index for lookups by tenant.
CREATE INDEX IF NOT EXISTS idx_host_liveness_tenant ON host_liveness (tenant);

-- Index for lookup by session_id (bounded session liveness check).
CREATE INDEX IF NOT EXISTS idx_host_liveness_session ON host_liveness (session_id)
    WHERE session_id != '';
