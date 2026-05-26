CREATE TABLE IF NOT EXISTS org_monitor_states (
    id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    health_class TEXT NOT NULL DEFAULT 'unhealthy',
    severity TEXT NOT NULL DEFAULT 'warning',
    color TEXT NOT NULL DEFAULT '#f59e0b',
    position INTEGER NOT NULL DEFAULT 0,
    is_builtin INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_monitor_states_org ON org_monitor_states(org_id);

CREATE TABLE IF NOT EXISTS monitor_match_rules (
    id TEXT PRIMARY KEY,
    monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    position INTEGER NOT NULL DEFAULT 0,
    status_code INTEGER,
    status_code_min INTEGER,
    status_code_max INTEGER,
    body_match TEXT NOT NULL DEFAULT '',
    body_match_mode TEXT NOT NULL DEFAULT 'contains',
    header_match TEXT NOT NULL DEFAULT '',
    header_value TEXT NOT NULL DEFAULT '',
    state_id TEXT NOT NULL REFERENCES org_monitor_states(id)
);

CREATE INDEX IF NOT EXISTS idx_match_rules_monitor ON monitor_match_rules(monitor_id);
