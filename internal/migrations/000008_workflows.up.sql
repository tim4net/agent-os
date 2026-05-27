-- Migration 008: Workflows and task subtasks

-- Add parent_task_id to tasks for subtask support
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS parent_task_id UUID REFERENCES tasks(id) ON DELETE CASCADE;

-- Workflows: reusable multi-step AI workflows
CREATE TABLE workflows (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    description TEXT,
    steps       JSONB NOT NULL DEFAULT '[]',
    agent_id    UUID REFERENCES agents(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

-- Workflow runs: track execution of workflows
CREATE TABLE workflow_runs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id UUID REFERENCES workflows(id) ON DELETE CASCADE,
    status      TEXT DEFAULT 'pending',
    current_step INT DEFAULT 0,
    result      JSONB DEFAULT '{}',
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);
