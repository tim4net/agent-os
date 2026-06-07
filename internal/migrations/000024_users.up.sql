-- Migration 024: users — the human identity + ownership boundary (Phase 1 spine,
-- agentos-multiuser-epic-plan.md).
--
-- AgentOS is multi-HUMAN (Tim + trusted collaborators/family), NOT Rewst multi-org.
-- A user is the auth principal and the top of the ownership hierarchy
-- (User -> Tenant -> Project -> data plane). This migration introduces ONLY the
-- user entity + the seed owner-0 row. Backfilling owner_id onto data-plane tables
-- is a SEPARATE later slice (kept out to keep this diff surgical) — the seam is the
-- hard part and is migrated table-by-table once a real user exists to point at.
--
-- IDENTITY: login is the verified, proxy-injected identity string (Tailscale login
-- name for v1, e.g. "tim@github" / "tim.fournet@…"). It is the natural key the
-- auth middleware looks up / lazily creates a user by. It is UNIQUE and immutable;
-- the surrogate UUID id is what data-plane rows will FK to as owner_id, so a login
-- rename never orphans owned rows. The trusted header is the ONLY identity source
-- (ADR: API must be unreachable directly so the header can't be spoofed).

CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    login         TEXT NOT NULL UNIQUE CHECK (length(btrim(login)) > 0),
    display_name  TEXT NOT NULL DEFAULT '',
    is_active     BOOLEAN NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- NOTE: login's UNIQUE constraint already creates a btree index, so no separate
-- idx_users_login is needed (it would only add write/storage overhead).

-- Seed owner-0: Tim, the existing sole owner of every data-plane row written before
-- multi-user. A fixed UUID makes the later owner_id backfill (UPDATE … SET owner_id =
-- '00000000-0000-0000-0000-000000000001') deterministic and reproducible across envs.
-- login 'tim' is a placeholder mapped from the v1 Tailscale identity; the middleware
-- treats a matching trusted header as this row rather than creating a duplicate.
INSERT INTO users (id, login, display_name)
VALUES ('00000000-0000-0000-0000-000000000001', 'tim', 'Tim Fournet')
ON CONFLICT (login) DO NOTHING;
