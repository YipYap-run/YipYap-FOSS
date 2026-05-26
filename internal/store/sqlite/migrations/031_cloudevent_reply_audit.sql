-- SQLite mirror of the Phase-5 reply audit log. JSONB collapses to TEXT
-- per sqlite conventions, and timestamps are RFC-3339 strings.

CREATE TABLE IF NOT EXISTS cloudevent_reply_audit (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    reply_id       TEXT    NOT NULL,
    reply_type     TEXT    NOT NULL,
    reply_source   TEXT    NOT NULL,
    reply_sub      TEXT    NOT NULL DEFAULT '',
    alert_id       TEXT    NOT NULL DEFAULT '',
    monitor_id     TEXT    NOT NULL DEFAULT '',
    org_id         TEXT    NOT NULL,
    channel_id     TEXT    NOT NULL DEFAULT '',
    outcome        TEXT    NOT NULL,
    before_state   TEXT    NOT NULL DEFAULT '{}',
    after_state    TEXT    NOT NULL DEFAULT '{}',
    received_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_cloudevent_reply_audit_org_received
    ON cloudevent_reply_audit (org_id, received_at DESC);

CREATE INDEX IF NOT EXISTS idx_cloudevent_reply_audit_alert
    ON cloudevent_reply_audit (alert_id, received_at DESC);

CREATE INDEX IF NOT EXISTS idx_cloudevent_reply_audit_channel
    ON cloudevent_reply_audit (channel_id, received_at DESC);

CREATE INDEX IF NOT EXISTS idx_cloudevent_reply_audit_received
    ON cloudevent_reply_audit (received_at);
