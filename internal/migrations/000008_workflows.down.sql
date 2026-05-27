-- Rollback Migration 008: Workflows and task subtasks

DROP TABLE IF EXISTS workflow_runs;
DROP TABLE IF EXISTS workflows;
ALTER TABLE tasks DROP COLUMN IF EXISTS parent_task_id;
