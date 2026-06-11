-- Migration 026: add project_id to memory_index for per-project scoping.
--
-- The memory_index table is populated by the Obsidian vault indexer every 5
-- minutes.  Without a project_id FK, chat RAG searches return notes from ALL
-- projects with no isolation.  This migration adds a nullable project_id column
-- so the indexer can tag each note with its owning project (derived from the
-- file path prefix).  Files that don't match any known project remain NULL.

ALTER TABLE memory_index ADD COLUMN project_id UUID REFERENCES projects(id);
CREATE INDEX idx_memory_index_project_id ON memory_index(project_id);
