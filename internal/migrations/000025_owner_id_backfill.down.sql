-- Migration 025 down: remove owner_id from all data-plane tables.

ALTER TABLE agents DROP COLUMN IF EXISTS owner_id;
ALTER TABLE projects DROP COLUMN IF EXISTS owner_id;
ALTER TABLE work_events DROP COLUMN IF EXISTS owner_id;
ALTER TABLE tasks DROP COLUMN IF EXISTS owner_id;
ALTER TABLE goals DROP COLUMN IF EXISTS owner_id;
ALTER TABLE skills DROP COLUMN IF EXISTS owner_id;
ALTER TABLE conversations DROP COLUMN IF EXISTS owner_id;
ALTER TABLE artifacts DROP COLUMN IF EXISTS owner_id;
ALTER TABLE delegations DROP COLUMN IF EXISTS owner_id;
ALTER TABLE pipeline_items DROP COLUMN IF EXISTS owner_id;
ALTER TABLE workflows DROP COLUMN IF EXISTS owner_id;
ALTER TABLE resources DROP COLUMN IF EXISTS owner_id;
ALTER TABLE tracker_items DROP COLUMN IF EXISTS owner_id;
ALTER TABLE app_instances DROP COLUMN IF EXISTS owner_id;
ALTER TABLE host_liveness DROP COLUMN IF EXISTS owner_id;
ALTER TABLE ingest_keys DROP COLUMN IF EXISTS owner_id;
ALTER TABLE messages DROP COLUMN IF EXISTS owner_id;
ALTER TABLE agent_grants DROP COLUMN IF EXISTS owner_id;
ALTER TABLE workflow_runs DROP COLUMN IF EXISTS owner_id;
ALTER TABLE memory_index DROP COLUMN IF EXISTS owner_id;
