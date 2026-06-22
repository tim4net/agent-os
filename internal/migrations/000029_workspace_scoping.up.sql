-- Migration 029: workspace scoping (issue #134).
--
-- A "workspace" is a project. To tie agents, memory, and artifacts together
-- per project we add a nullable project_id FK to the agents and artifacts
-- tables, mirroring what migration 026 already did for memory_index. NULL
-- means "global / not yet assigned to a workspace", preserving every existing
-- row. ON DELETE SET NULL so deleting a project never cascades into losing
-- agents or artifacts.
--
-- IF NOT EXISTS guards make this migration safe to (re)apply on databases
-- that already carry the columns (e.g. long-lived test fixtures).

ALTER TABLE agents
    ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES projects(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_agents_project_id ON agents(project_id);

ALTER TABLE artifacts
    ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES projects(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_artifacts_project_id ON artifacts(project_id);
