-- Phase-5 state tables supporting the mutating reply handlers.
-- Each reply type that creates side-state has its own table so inserts
-- stay independent and schema churn is local.

CREATE TABLE IF NOT EXISTS alert_suppressions (
    id              TEXT    PRIMARY KEY,
    org_id          TEXT    NOT NULL,
    alert_id        TEXT    NOT NULL DEFAULT '',
    monitor_id      TEXT    NOT NULL DEFAULT '',
    match_filter    TEXT    NOT NULL DEFAULT '{}',
    expires_at      TEXT    NOT NULL,
    reason          TEXT    NOT NULL DEFAULT '',
    source_reply_id TEXT    NOT NULL DEFAULT '',
    created_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_alert_suppressions_org_expires
    ON alert_suppressions (org_id, expires_at);
CREATE INDEX IF NOT EXISTS idx_alert_suppressions_alert
    ON alert_suppressions (alert_id);

CREATE TABLE IF NOT EXISTS alert_remediations (
    remediation_id   TEXT PRIMARY KEY,
    alert_id         TEXT NOT NULL,
    status           TEXT NOT NULL,
    started_at       TEXT NOT NULL,
    completed_at     TEXT,
    expires_at       TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    message          TEXT NOT NULL DEFAULT '',
    artifact_url     TEXT NOT NULL DEFAULT '',
    source_reply_id  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_alert_remediations_alert_status
    ON alert_remediations (alert_id, status);
CREATE INDEX IF NOT EXISTS idx_alert_remediations_expires
    ON alert_remediations (expires_at);

CREATE TABLE IF NOT EXISTS alert_routing_rules (
    id                TEXT PRIMARY KEY,
    org_id            TEXT NOT NULL,
    target_channel_id TEXT NOT NULL,
    match_filter      TEXT NOT NULL DEFAULT '{}',
    expires_at        TEXT NOT NULL,
    reason            TEXT NOT NULL DEFAULT '',
    source_reply_id   TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_alert_routing_rules_org_expires
    ON alert_routing_rules (org_id, expires_at);

CREATE TABLE IF NOT EXISTS alert_status_page_refs (
    id              TEXT PRIMARY KEY,
    alert_id        TEXT NOT NULL,
    provider        TEXT NOT NULL,
    url             TEXT NOT NULL DEFAULT '',
    public_message  TEXT NOT NULL DEFAULT '',
    updated_at      TEXT NOT NULL,
    source_reply_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_alert_status_page_refs_alert
    ON alert_status_page_refs (alert_id);

ALTER TABLE alerts ADD COLUMN duplicate_of TEXT NOT NULL DEFAULT '';
ALTER TABLE alerts ADD COLUMN owner_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE alerts ADD COLUMN owner_id TEXT NOT NULL DEFAULT '';
ALTER TABLE alerts ADD COLUMN ack_reason_code TEXT NOT NULL DEFAULT '';
ALTER TABLE alerts ADD COLUMN ack_reference_id TEXT NOT NULL DEFAULT '';
