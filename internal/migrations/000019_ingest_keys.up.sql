-- WP-A2: Durable per-tenant ingest-key store.
-- Replaces the env-backed AGENTOS_INGEST_KEYS map with a hashed key table.
-- Keys are stored as SHA-256 hashes — raw keys are never persisted or logged.
-- Each key is bound to one tenant; the server enforces that an event's tenant
-- field matches the key's allowed tenant (contract §1 / ADR-002 D6).

CREATE TABLE IF NOT EXISTS ingest_keys (
    id          BIGSERIAL       PRIMARY KEY,
    key_hash    TEXT            NOT NULL UNIQUE,          -- SHA-256 hex of the raw key
    tenant      TEXT            NOT NULL,                 -- allowed tenant (personal, dayjob, ...)
    label       TEXT            NOT NULL DEFAULT '',      -- human-friendly label (e.g. "macbook-hermes")
    created_at  TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    revoked_at  TIMESTAMPTZ     NULL                      -- NULL = active, non-NULL = revoked
);

-- Index for fast lookups by key_hash (the hot path on every ingest).
CREATE INDEX IF NOT EXISTS idx_ingest_keys_key_hash ON ingest_keys (key_hash);

-- Index for admin listing by tenant.
CREATE INDEX IF NOT EXISTS idx_ingest_keys_tenant ON ingest_keys (tenant);
