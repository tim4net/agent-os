CREATE TABLE IF NOT EXISTS run_log (
    id          BIGSERIAL       PRIMARY KEY,
    ts          TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    event_type  TEXT            NOT NULL DEFAULT '',
    pr_ref      TEXT            NOT NULL DEFAULT '',
    wp_ref      TEXT            NOT NULL DEFAULT '',
    summary     TEXT,
    payload     JSONB
);

CREATE TABLE IF NOT EXISTS findings (
    id           BIGSERIAL       PRIMARY KEY,
    ts           TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    pr_ref       TEXT            NOT NULL DEFAULT '',
    wp_ref       TEXT            NOT NULL DEFAULT '',
    gate         INT             NOT NULL DEFAULT 0,
    author_agent TEXT            NOT NULL DEFAULT '',
    model        TEXT            NOT NULL DEFAULT '',
    severity     TEXT            NOT NULL DEFAULT 'info',
    class        TEXT            NOT NULL DEFAULT '',
    root_cause   TEXT,
    summary      TEXT
);

CREATE INDEX IF NOT EXISTS idx_run_log_ts ON run_log (ts DESC);
CREATE INDEX IF NOT EXISTS idx_findings_ts ON findings (ts DESC);
CREATE INDEX IF NOT EXISTS idx_findings_class ON findings (class);
CREATE INDEX IF NOT EXISTS idx_findings_wp_ref ON findings (wp_ref);
