-- Rollback migration 018: agent session indexes (WP-J).
DROP INDEX IF EXISTS idx_work_events_session_end;
DROP INDEX IF EXISTS idx_work_events_tenant_session;
