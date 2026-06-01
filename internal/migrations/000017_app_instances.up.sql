-- Migration 017: app_instances table (WP-I App instance registry, ADR-003).
-- Tracks registered application instances with real HTTP health probing.
-- Instances are created from server.started work-events (contract §4) or
-- via manual POST. Status is derived from actual probes, never from DB flags
-- (the anti-fake-status rule: contract §4 "running requires positive proof").
-- Tenant-scoped per ADR-002.

CREATE TABLE app_instances (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    harness         TEXT NOT NULL DEFAULT '',                    -- emitting agent harness
    session_id      TEXT NOT NULL DEFAULT '',                    -- session that created/last-updated
    host            TEXT NOT NULL DEFAULT '',                     -- hostname (liveness keying)
    pid             INT,                                         -- process PID (supervised liveness)
    label           TEXT NOT NULL DEFAULT '',                     -- human-readable label
    health_url      TEXT NOT NULL DEFAULT '',                     -- URL to probe for health (the REAL probe target)
    branch          TEXT,                                         -- git branch
    sha             TEXT,                                         -- git SHA
    cwd             TEXT,                                         -- working directory
    tenant          TEXT NOT NULL DEFAULT 'personal',            -- owner tenant (ADR-002)
    status          TEXT NOT NULL DEFAULT 'unknown',             -- up|down|unknown — NEVER set directly, always from probe result
    last_probed_at  TIMESTAMPTZ,                                  -- last probe timestamp (NULL = never probed)
    last_heartbeat  TIMESTAMPTZ,                                  -- from latest work-event received_at for this instance
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Unique constraint: one instance per (host, health_url, tenant) combination.
    -- Prevents duplicate registrations of the same service.
    CONSTRAINT uq_app_instances_host_url_tenant UNIQUE (host, health_url, tenant)
);

-- Index for listing instances by tenant (read-only dashboard queries).
CREATE INDEX idx_app_instances_tenant ON app_instances(tenant);

-- Index for finding instances by host.
CREATE INDEX idx_app_instances_host ON app_instances(host);

-- Index for finding instances by status (dashboard filtering).
CREATE INDEX idx_app_instances_status ON app_instances(status);

-- Composite index for upsert on server.started events (host + tenant).
CREATE INDEX idx_app_instances_host_tenant ON app_instances(host, tenant);
