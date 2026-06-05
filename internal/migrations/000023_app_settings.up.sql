-- Migration 023: app_settings — orchestrator control-plane key/value store.
-- Backs the Settings page (WP-SETTINGS): provider API keys + general app config.
--
-- Secrets (API keys, auth tokens) are stored ENCRYPTED at rest in enc_value
-- (AES-256-GCM, nonce-prefixed) — never in plaintext. `last4` holds only the
-- final 4 characters for masked display ("••••1234"). Non-secret settings use
-- the plaintext `value` column. The API never returns a secret's plaintext;
-- it returns is_set + last4 only.
--
-- Single-tenant by design: this is Tim's private orchestrator config, not
-- per-tenant payload data (ADR-002). No tenant column.

CREATE TABLE app_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL DEFAULT '',
    is_secret  BOOLEAN NOT NULL DEFAULT false,
    enc_value  BYTEA,
    last4      TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
