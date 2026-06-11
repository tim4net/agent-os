-- Migration 025: owner_id backfill — add owner_id UUID FK to every data-plane
-- table, backfill to owner-0, set NOT NULL, add indexes.
--
-- The owner_id column is the multi-user ownership seam (issue #92, Phase 1
-- spine). Every data-plane row is owned by exactly one user (FK → users.id).
-- The seed owner-0 UUID ('00000000-0000-0000-0000-000000000001') was created
-- in migration 024. All pre-existing rows are backfilled to owner-0.
--
-- Design choices:
--   - owner_id is NOT NULL with a DEFAULT → existing app code that doesn't
--     pass owner_id yet will still work (gets owner-0). Once every write-path
--     is wired to extract owner_id from context, the DEFAULT can be removed.
--   - Separate ALTER TABLE + UPDATE + ALTER TABLE … SET NOT NULL pattern
--     avoids the "cannot add NOT NULL column without default" problem and
--     works cleanly with existing data.
--   - Indexes on owner_id for every table support efficient per-owner queries
--     (the core isolation invariant: rows from one owner never appear to another).

-- owner-0 UUID constant (matches migration 024 seed).
-- DO NOT repeat the literal; use a psql variable or a CTE where possible.
-- Here we inline it for migration SQL simplicity.

--------------------------------------------------------------------------------
-- agents
--------------------------------------------------------------------------------
ALTER TABLE agents ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
    REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_agents_owner_id ON agents(owner_id);

--------------------------------------------------------------------------------
-- projects
--------------------------------------------------------------------------------
ALTER TABLE projects ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
    REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_projects_owner_id ON projects(owner_id);

--------------------------------------------------------------------------------
-- work_events
--------------------------------------------------------------------------------
ALTER TABLE work_events ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
    REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_work_events_owner_id ON work_events(owner_id);

--------------------------------------------------------------------------------
-- tasks
--------------------------------------------------------------------------------
ALTER TABLE tasks ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
    REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_tasks_owner_id ON tasks(owner_id);

--------------------------------------------------------------------------------
-- goals
--------------------------------------------------------------------------------
ALTER TABLE goals ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
    REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_goals_owner_id ON goals(owner_id);

--------------------------------------------------------------------------------
-- skills
--------------------------------------------------------------------------------
ALTER TABLE skills ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
    REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_skills_owner_id ON skills(owner_id);

--------------------------------------------------------------------------------
-- conversations
--------------------------------------------------------------------------------
ALTER TABLE conversations ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
    REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_conversations_owner_id ON conversations(owner_id);

--------------------------------------------------------------------------------
-- artifacts
--------------------------------------------------------------------------------
ALTER TABLE artifacts ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
    REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_artifacts_owner_id ON artifacts(owner_id);

--------------------------------------------------------------------------------
-- delegations
--------------------------------------------------------------------------------
ALTER TABLE delegations ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
    REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_delegations_owner_id ON delegations(owner_id);

--------------------------------------------------------------------------------
-- pipeline_items
--------------------------------------------------------------------------------
ALTER TABLE pipeline_items ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
    REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_pipeline_items_owner_id ON pipeline_items(owner_id);

--------------------------------------------------------------------------------
-- workflows
--------------------------------------------------------------------------------
ALTER TABLE workflows ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
    REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_workflows_owner_id ON workflows(owner_id);

--------------------------------------------------------------------------------
-- resources
--------------------------------------------------------------------------------
ALTER TABLE resources ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
    REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_resources_owner_id ON resources(owner_id);

--------------------------------------------------------------------------------
-- tracker_items
--------------------------------------------------------------------------------
ALTER TABLE tracker_items ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
    REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_tracker_items_owner_id ON tracker_items(owner_id);

--------------------------------------------------------------------------------
-- app_instances
--------------------------------------------------------------------------------
ALTER TABLE app_instances ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
    REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_app_instances_owner_id ON app_instances(owner_id);

--------------------------------------------------------------------------------
-- host_liveness
--------------------------------------------------------------------------------
ALTER TABLE host_liveness ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
    REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_host_liveness_owner_id ON host_liveness(owner_id);

--------------------------------------------------------------------------------
-- ingest_keys
--------------------------------------------------------------------------------
ALTER TABLE ingest_keys ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
    REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_ingest_keys_owner_id ON ingest_keys(owner_id);

--------------------------------------------------------------------------------
-- messages (via conversation → owner)
--------------------------------------------------------------------------------
ALTER TABLE messages ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
    REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_messages_owner_id ON messages(owner_id);

--------------------------------------------------------------------------------
-- agent_grants
--------------------------------------------------------------------------------
ALTER TABLE agent_grants ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
    REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_agent_grants_owner_id ON agent_grants(owner_id);

--------------------------------------------------------------------------------
-- workflow_runs
--------------------------------------------------------------------------------
ALTER TABLE workflow_runs ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
    REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_workflow_runs_owner_id ON workflow_runs(owner_id);

--------------------------------------------------------------------------------
-- memory_index
--------------------------------------------------------------------------------
ALTER TABLE memory_index ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001'
    REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_memory_index_owner_id ON memory_index(owner_id);
