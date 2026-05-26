-- Prevent duplicate active alerts for the same monitor.
-- Only one alert in 'firing' or 'acknowledged' state may exist per monitor.
CREATE UNIQUE INDEX IF NOT EXISTS idx_alerts_active_monitor
    ON alerts(monitor_id) WHERE status IN ('firing', 'acknowledged');

-- Notification dedup ledger for cross-instance deduplication.
CREATE TABLE IF NOT EXISTS notification_dedup (
    dedupe_key  TEXT PRIMARY KEY,
    expires_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_notification_dedup_expires ON notification_dedup(expires_at);

-- Notification outbox for at-least-once delivery.
-- Jobs are written here by the escalation engine and claimed by dispatcher workers.
CREATE TABLE IF NOT EXISTS notification_outbox (
    id          TEXT PRIMARY KEY,
    payload     TEXT NOT NULL,         -- JSON-encoded NotificationJob
    status      TEXT NOT NULL DEFAULT 'pending',  -- pending | claimed | done | dead
    claimed_by  TEXT,                  -- instance identifier
    claimed_at  TEXT,                  -- when the worker claimed it
    attempts    INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL,
    completed_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_notification_outbox_status ON notification_outbox(status);
CREATE INDEX IF NOT EXISTS idx_notification_outbox_claimed ON notification_outbox(status, claimed_at);
