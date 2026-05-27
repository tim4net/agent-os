CREATE TABLE IF NOT EXISTS delegations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_agent_id UUID NOT NULL REFERENCES agents(id),
    child_agent_name TEXT NOT NULL,
    task_goal TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','completed','failed','interrupted')),
    result_summary TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    metadata JSONB DEFAULT '{}'
);

CREATE INDEX idx_delegations_parent ON delegations(parent_agent_id);
CREATE INDEX idx_delegations_status ON delegations(status);
CREATE INDEX idx_delegations_created ON delegations(created_at DESC);
