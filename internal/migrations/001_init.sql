-- Agent OS Database Schema
-- Migration 001: Initial schema

-- Enable UUID generation
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Agents registered in the system
CREATE TABLE agents (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    harness     TEXT NOT NULL DEFAULT 'generic',
    base_url    TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'unknown',
    metadata    JSONB DEFAULT '{}',
    last_seen   TIMESTAMPTZ,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

-- AI-generated or agent-produced artifacts
CREATE TABLE artifacts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id    UUID REFERENCES agents(id) ON DELETE SET NULL,
    type        TEXT NOT NULL,
    title       TEXT,
    description TEXT,
    file_path   TEXT,
    mime_type   TEXT,
    metadata    JSONB DEFAULT '{}',
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

-- Chat conversations
CREATE TABLE conversations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id    UUID REFERENCES agents(id) ON DELETE CASCADE,
    title       TEXT,
    metadata    JSONB DEFAULT '{}',
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

-- Individual messages in conversations
CREATE TABLE messages (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID REFERENCES conversations(id) ON DELETE CASCADE,
    role            TEXT NOT NULL,
    content         TEXT NOT NULL,
    metadata        JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

-- Kanban tasks
CREATE TABLE tasks (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id    UUID REFERENCES agents(id) ON DELETE SET NULL,
    title       TEXT NOT NULL,
    description TEXT,
    status      TEXT NOT NULL DEFAULT 'backlog',
    priority    INTEGER DEFAULT 0,
    metadata    JSONB DEFAULT '{}',
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

-- Goals / OKRs
CREATE TABLE goals (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title       TEXT NOT NULL,
    description TEXT,
    status      TEXT NOT NULL DEFAULT 'active',
    progress    REAL DEFAULT 0,
    target_date DATE,
    metadata    JSONB DEFAULT '{}',
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

-- Content pipeline items
CREATE TABLE pipeline_items (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type        TEXT NOT NULL,
    title       TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'draft',
    content     TEXT,
    metadata    JSONB DEFAULT '{}',
    agent_id    UUID REFERENCES agents(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

-- Memory index (searchable Obsidian notes)
CREATE TABLE memory_index (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    file_path   TEXT NOT NULL UNIQUE,
    title       TEXT,
    content     TEXT,
    tags        TEXT[],
    last_indexed TIMESTAMPTZ DEFAULT NOW()
);

-- Full text search index
CREATE INDEX idx_memory_fts ON memory_index USING gin(to_tsvector('english', coalesce(content, '')));

-- Seed initial agents
INSERT INTO agents (name, display_name, harness, base_url, status) VALUES
    ('roux', 'Roux', 'hermes', 'http://100.66.94.15:8642', 'unknown'),
    ('crawbot', 'Crawbot', 'openclaw', 'http://100.68.106.15:2222', 'unknown'),
    ('litellm', 'LiteLLM', 'litellm', 'http://100.113.84.43:4000', 'unknown');
