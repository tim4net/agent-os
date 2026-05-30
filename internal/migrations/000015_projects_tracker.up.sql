-- Migration 015: pluggable tracker source per project (ADR-001 D5, ADR-002 tenancy).
-- Adds the tracker source + external linkage to projects. tenant already exists from 014.

ALTER TABLE projects
    ADD COLUMN tracker      TEXT NOT NULL DEFAULT 'agent_os_native',  -- shortcut|github_issues|obsidian|agent_os_native
    ADD COLUMN external_ref TEXT,                                     -- e.g. Shortcut project id / gh repo slug
    ADD COLUMN repo_url     TEXT;                                     -- origin remote, for correlation/launch

-- Constrain tracker to the frozen vocabulary.
ALTER TABLE projects
    ADD CONSTRAINT projects_tracker_check
    CHECK (tracker IN ('shortcut', 'github_issues', 'obsidian', 'agent_os_native'));
