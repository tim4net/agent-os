-- Migration 009: Agent roles, system prompts, and persona config

ALTER TABLE agents ADD COLUMN IF NOT EXISTS role TEXT DEFAULT '';
ALTER TABLE agents ADD COLUMN IF NOT EXISTS system_prompt TEXT DEFAULT '';
ALTER TABLE agents ADD COLUMN IF NOT EXISTS persona JSONB DEFAULT '{}';
