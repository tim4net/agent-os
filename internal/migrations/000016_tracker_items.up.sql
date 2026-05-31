-- Migration 016: tracker_items table (WP-E Shortcut reader, contract §8).
-- Stores read-only mirrors of external tracker stories with synced_at.
-- Tenant-scoped per ADR-002; external_ref format SC-<n> (Shortcut) or #<n> (GitHub).

CREATE TABLE tracker_items (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id    UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    external_ref  TEXT NOT NULL,                       -- SC-<n> or #<n>
    title         TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT '',            -- canonical status from tracker
    item_type     TEXT NOT NULL DEFAULT 'story',       -- story|bug|chore|task|feature
    canonical_url TEXT,                                 -- link back to canonical source
    payload       JSONB DEFAULT '{}',                  -- raw tracker-specific metadata
    tenant        TEXT NOT NULL,                        -- owner tenant (ADR-002)
    synced_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),  -- last successful sync
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- A project can only have one tracker item per external_ref (upsert key).
    UNIQUE (project_id, external_ref)
);

-- Index for listing items by tenant (read-only dashboard queries).
CREATE INDEX idx_tracker_items_tenant ON tracker_items(tenant);

-- Index for listing items by project.
CREATE INDEX idx_tracker_items_project ON tracker_items(project_id);

-- Index for correlation: lookup by external_ref across tenant.
CREATE INDEX idx_tracker_items_external_ref ON tracker_items(external_ref);
