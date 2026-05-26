CREATE TABLE IF NOT EXISTS cloudevent_ingest_idempotency (
    monitor_id TEXT NOT NULL,
    event_id   TEXT NOT NULL,
    seen_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (monitor_id, event_id)
);

CREATE INDEX IF NOT EXISTS idx_cloudevent_ingest_idempotency_seen_at
    ON cloudevent_ingest_idempotency (seen_at);
