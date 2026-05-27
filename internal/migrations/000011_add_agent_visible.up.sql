-- Add visible column to agents table for backend-controlled visibility.
-- Infrastructure agents (litellm) are hidden from the frontend agent list.
ALTER TABLE agents ADD COLUMN IF NOT EXISTS visible boolean NOT NULL DEFAULT true;

-- Hide LiteLLM from frontend — it's infrastructure, not a chat target
UPDATE agents SET visible = false WHERE harness = 'litellm';
