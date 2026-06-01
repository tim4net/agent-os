-- Migration 018: agent session indexes (WP-J Live fleet monitor).
-- Liveness is a PURE FUNCTION of persisted work_events + server clock (contract §4).
-- No separate agent_sessions table — that would be fake state. This migration adds
-- indexes to support efficient fleet status derivation queries.
-- Tenant-scoped per ADR-002.

-- Index for tenant-scoped fleet listing: find all sessions for a tenant
-- ordered by recency (for the GET /api/fleet endpoint).
CREATE INDEX idx_work_events_tenant_session
    ON work_events (tenant, harness, session_id, received_at DESC);

-- Partial index for session.end events (terminal events).
-- The liveness derivation needs to check if a terminal event exists;
-- this makes that check fast without scanning the full session history.
CREATE INDEX idx_work_events_session_end
    ON work_events (harness, session_id, received_at DESC)
    WHERE kind = 'session.end';
