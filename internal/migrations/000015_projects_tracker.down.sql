-- Migration 015 rollback.
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_tracker_check;
ALTER TABLE projects
    DROP COLUMN IF EXISTS repo_url,
    DROP COLUMN IF EXISTS external_ref,
    DROP COLUMN IF EXISTS tracker;
