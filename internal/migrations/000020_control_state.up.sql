-- WP-O1: Orchestrator engine — control state and work-unit queue.

CREATE TYPE control_mode AS ENUM ('continuous', 'tick', 'stopped');
CREATE TYPE work_unit_status AS ENUM ('queued', 'in_flight', 'done', 'failed');

-- Singleton row: always exactly one row controlling orchestrator behaviour.
CREATE TABLE control_state (
    mode            control_mode NOT NULL DEFAULT 'stopped',
    cadence_seconds INT          NOT NULL DEFAULT 60,
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Work-unit queue: the orchestrator claims rows via SKIP LOCKED.
CREATE TABLE work_units (
    id           BIGSERIAL        PRIMARY KEY,
    wp_ref       TEXT             NOT NULL DEFAULT '',
    status       work_unit_status NOT NULL DEFAULT 'queued',
    payload      JSONB,
    claimed_at   TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error        TEXT,
    created_at   TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);

-- Seed the singleton row.
INSERT INTO control_state (mode, cadence_seconds) VALUES ('stopped', 60);
