-- Migration 014 rollback.
DROP INDEX IF EXISTS idx_work_events_received_at;
DROP INDEX IF EXISTS idx_work_events_correlation;
DROP INDEX IF EXISTS idx_work_events_session;
DROP TABLE IF EXISTS work_events;
DROP TABLE IF EXISTS projects;
