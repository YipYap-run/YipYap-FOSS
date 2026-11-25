CREATE TABLE IF NOT EXISTS alert_paged_targets (
    alert_id   TEXT NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    target_key TEXT NOT NULL,
    kind       TEXT NOT NULL,
    user_id    TEXT NOT NULL DEFAULT '',
    method_id  TEXT NOT NULL DEFAULT '',
    channel_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (alert_id, target_key)
);

CREATE INDEX IF NOT EXISTS idx_alert_paged_targets_alert ON alert_paged_targets(alert_id);
