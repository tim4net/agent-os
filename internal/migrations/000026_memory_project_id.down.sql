DROP INDEX IF EXISTS idx_memory_index_project_id;
ALTER TABLE memory_index DROP COLUMN IF EXISTS project_id;
