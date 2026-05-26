-- 001_init.sql: Initial schema for yipyap-alerts

CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Organizations
CREATE TABLE IF NOT EXISTS orgs (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    plan TEXT NOT NULL DEFAULT 'free',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- Users
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES orgs(id),
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL DEFAULT 'member',
    phone TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_users_org_id ON users(org_id);

-- OIDC Connections
CREATE TABLE IF NOT EXISTS oidc_connections (
    id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES orgs(id),
    provider TEXT NOT NULL,
    client_id TEXT NOT NULL,
    client_secret TEXT NOT NULL DEFAULT '',
    issuer_url TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_oidc_connections_org_id ON oidc_connections(org_id);

-- User OIDC Links
CREATE TABLE IF NOT EXISTS user_oidc_links (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    oidc_connection_id TEXT NOT NULL REFERENCES oidc_connections(id) ON DELETE CASCADE,
    external_subject_id TEXT NOT NULL,
    PRIMARY KEY (oidc_connection_id, external_subject_id)
);
CREATE INDEX IF NOT EXISTS idx_user_oidc_links_user_id ON user_oidc_links(user_id);

-- Escalation Policies
CREATE TABLE IF NOT EXISTS escalation_policies (
    id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES orgs(id),
    name TEXT NOT NULL,
    loop INTEGER NOT NULL DEFAULT 0,
    max_loops INTEGER
);
CREATE INDEX IF NOT EXISTS idx_escalation_policies_org_id ON escalation_policies(org_id);

-- Escalation Steps
CREATE TABLE IF NOT EXISTS escalation_steps (
    id TEXT PRIMARY KEY,
    policy_id TEXT NOT NULL REFERENCES escalation_policies(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    wait_seconds INTEGER NOT NULL DEFAULT 0,
    repeat_count INTEGER NOT NULL DEFAULT 0,
    repeat_interval_seconds INTEGER NOT NULL DEFAULT 0,
    is_terminal INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_escalation_steps_policy_id ON escalation_steps(policy_id);

-- Step Targets
CREATE TABLE IF NOT EXISTS step_targets (
    id TEXT PRIMARY KEY,
    step_id TEXT NOT NULL REFERENCES escalation_steps(id) ON DELETE CASCADE,
    target_type TEXT NOT NULL,
    target_id TEXT NOT NULL DEFAULT '',
    channel TEXT NOT NULL DEFAULT '',
    simultaneous INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_step_targets_step_id ON step_targets(step_id);

-- Monitors
CREATE TABLE IF NOT EXISTS monitors (
    id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES orgs(id),
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    config TEXT NOT NULL DEFAULT '{}',
    interval_seconds INTEGER NOT NULL DEFAULT 60,
    timeout_seconds INTEGER NOT NULL DEFAULT 10,
    regions TEXT NOT NULL DEFAULT '[]',
    escalation_policy_id TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_monitors_org_id ON monitors(org_id);

-- Monitor Labels
CREATE TABLE IF NOT EXISTS monitor_labels (
    monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    PRIMARY KEY (monitor_id, key)
);

-- Monitor Checks
CREATE TABLE IF NOT EXISTS monitor_checks (
    id TEXT PRIMARY KEY,
    monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    status TEXT NOT NULL,
    latency_ms INTEGER NOT NULL DEFAULT 0,
    status_code INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    tls_expiry_at TEXT,
    checked_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_monitor_checks_monitor_id ON monitor_checks(monitor_id);
CREATE INDEX IF NOT EXISTS idx_monitor_checks_checked_at ON monitor_checks(monitor_id, checked_at);

-- Monitor Rollups
CREATE TABLE IF NOT EXISTS monitor_rollups (
    monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    period TEXT NOT NULL,
    period_start TEXT NOT NULL,
    uptime_pct REAL NOT NULL DEFAULT 0,
    avg_latency_ms REAL NOT NULL DEFAULT 0,
    p95_latency_ms REAL NOT NULL DEFAULT 0,
    p99_latency_ms REAL NOT NULL DEFAULT 0,
    check_count INTEGER NOT NULL DEFAULT 0,
    failure_count INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (monitor_id, period, period_start)
);

-- Teams
CREATE TABLE IF NOT EXISTS teams (
    id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES orgs(id),
    name TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_teams_org_id ON teams(org_id);

-- Team Members
CREATE TABLE IF NOT EXISTS team_members (
    team_id TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    position INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (team_id, user_id)
);

-- Schedules
CREATE TABLE IF NOT EXISTS schedules (
    id TEXT PRIMARY KEY,
    team_id TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    rotation_interval TEXT NOT NULL DEFAULT 'weekly',
    rotation_interval_hours INTEGER NOT NULL DEFAULT 0,
    handoff_time TEXT NOT NULL DEFAULT '09:00',
    effective_from TEXT NOT NULL,
    timezone TEXT NOT NULL DEFAULT 'UTC'
);
CREATE INDEX IF NOT EXISTS idx_schedules_team_id ON schedules(team_id);

-- Schedule Overrides
CREATE TABLE IF NOT EXISTS schedule_overrides (
    id TEXT PRIMARY KEY,
    schedule_id TEXT NOT NULL REFERENCES schedules(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id),
    start_at TEXT NOT NULL,
    end_at TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_schedule_overrides_schedule_id ON schedule_overrides(schedule_id);

-- Alerts
CREATE TABLE IF NOT EXISTS alerts (
    id TEXT PRIMARY KEY,
    monitor_id TEXT NOT NULL REFERENCES monitors(id),
    org_id TEXT NOT NULL REFERENCES orgs(id),
    status TEXT NOT NULL DEFAULT 'firing',
    severity TEXT NOT NULL DEFAULT 'critical',
    started_at TEXT NOT NULL,
    acknowledged_at TEXT,
    acknowledged_by TEXT NOT NULL DEFAULT '',
    resolved_at TEXT,
    current_escalation_step TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_alerts_org_id ON alerts(org_id);
CREATE INDEX IF NOT EXISTS idx_alerts_monitor_id ON alerts(monitor_id);
CREATE INDEX IF NOT EXISTS idx_alerts_status ON alerts(status);

-- Alert Events
CREATE TABLE IF NOT EXISTS alert_events (
    id TEXT PRIMARY KEY,
    alert_id TEXT NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    channel TEXT NOT NULL DEFAULT '',
    target_user_id TEXT NOT NULL DEFAULT '',
    detail TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_alert_events_alert_id ON alert_events(alert_id);

-- Alert Escalation State
CREATE TABLE IF NOT EXISTS alert_escalation_state (
    alert_id TEXT PRIMARY KEY REFERENCES alerts(id) ON DELETE CASCADE,
    current_step_id TEXT NOT NULL,
    step_entered_at TEXT NOT NULL,
    retry_count INTEGER NOT NULL DEFAULT 0,
    last_notified_at TEXT,
    notifications_sent TEXT NOT NULL DEFAULT '[]'
);

-- Notification Channels
CREATE TABLE IF NOT EXISTS notification_channels (
    id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES orgs(id),
    type TEXT NOT NULL,
    name TEXT NOT NULL,
    config TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_notification_channels_org_id ON notification_channels(org_id);

-- Maintenance Windows
CREATE TABLE IF NOT EXISTS maintenance_windows (
    id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES orgs(id),
    monitor_id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    start_at TEXT NOT NULL,
    end_at TEXT NOT NULL,
    public INTEGER NOT NULL DEFAULT 0,
    suppress_alerts INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    created_by TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_maintenance_windows_org_id ON maintenance_windows(org_id);
CREATE INDEX IF NOT EXISTS idx_maintenance_windows_monitor_id ON maintenance_windows(monitor_id);

-- Status Pages
CREATE TABLE IF NOT EXISTS status_pages (
    id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES orgs(id),
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    custom_css TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_status_pages_org_id ON status_pages(org_id);

-- Status Page Monitors
CREATE TABLE IF NOT EXISTS status_page_monitors (
    status_page_id TEXT NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    position INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (status_page_id, monitor_id)
);

-- API Keys
CREATE TABLE IF NOT EXISTS api_keys (
    id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES orgs(id),
    name TEXT NOT NULL,
    key_hash TEXT NOT NULL UNIQUE,
    prefix TEXT NOT NULL,
    scopes TEXT NOT NULL DEFAULT '[]',
    created_by TEXT NOT NULL DEFAULT '',
    expires_at TEXT,
    created_at TEXT NOT NULL,
    last_used_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_api_keys_org_id ON api_keys(org_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys(key_hash);
