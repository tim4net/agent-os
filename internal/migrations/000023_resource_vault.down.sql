-- Rollback migration 023: resource vault + agent grants.
DROP TABLE IF EXISTS agent_grants;
DROP TABLE IF EXISTS resources;
