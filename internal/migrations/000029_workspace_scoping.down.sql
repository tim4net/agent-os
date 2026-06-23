-- Roll back migration 029: remove workspace scoping from agents + artifacts.

DROP INDEX IF EXISTS idx_artifacts_project_id;
ALTER TABLE artifacts DROP COLUMN IF EXISTS project_id;

DROP INDEX IF EXISTS idx_agents_project_id;
ALTER TABLE agents DROP COLUMN IF EXISTS project_id;
