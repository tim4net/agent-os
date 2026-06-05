-- Migration 023: resource vault + agent capability grants (replaces the flat
-- app_settings provider-key catalog — that catalog never reached main).
--
-- VAULT: every credential / integration / mcp_server the orchestrator configures
-- lives here ONCE, identified by a unique slug. Provider is a NON-unique attribute,
-- so multiple creds per provider are first-class (e.g. openrouter-personal,
-- openrouter-work both provider='openrouter'). Secrets are AES-256-GCM encrypted
-- at rest in enc_value / enc_config (nonce-prefixed); last4 holds only a masked tail.
--
-- GRANTS: default-deny capability model. An agent can use a resource ONLY if an
-- explicit agent_grants row exists. Revoking a grant removes the capability at the
-- next harness build. Single-tenant (Tim's private orchestrator); no tenant column.

CREATE TABLE resources (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug        TEXT NOT NULL UNIQUE,
    kind        TEXT NOT NULL CHECK (kind IN ('credential', 'integration', 'mcp_server')),
    label       TEXT NOT NULL DEFAULT '',
    provider    TEXT NOT NULL DEFAULT '',
    is_secret   BOOLEAN NOT NULL DEFAULT false,
    enc_value   BYTEA,
    enc_config  BYTEA,
    config      JSONB NOT NULL DEFAULT '{}',
    last4       TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'unset',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_resources_kind ON resources (kind);
CREATE INDEX idx_resources_provider ON resources (provider);

-- Default-deny capability grants: (agent, resource, scope).
CREATE TABLE agent_grants (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id    UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    resource_id UUID NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    scope       TEXT NOT NULL DEFAULT 'use',
    granted_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (agent_id, resource_id, scope)
);

CREATE INDEX idx_agent_grants_agent ON agent_grants (agent_id);
CREATE INDEX idx_agent_grants_resource ON agent_grants (resource_id);
