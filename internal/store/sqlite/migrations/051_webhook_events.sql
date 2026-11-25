-- Inbound webhook event ledger. A delivery takes a short processing lease
-- (status 'processing'); the row becomes 'done' only after the handler
-- succeeds, and a failed handler deletes it so the retry is processed.
-- Times are fixed-width millisecond UTC strings so they compare correctly.
CREATE TABLE IF NOT EXISTS webhook_events (
    event_key        TEXT PRIMARY KEY,
    status           TEXT NOT NULL,
    lease_token      TEXT NOT NULL,
    lease_expires_at TEXT NOT NULL,
    expires_at       TEXT NOT NULL,
    processed_at     TEXT
);

CREATE INDEX IF NOT EXISTS idx_webhook_events_expires ON webhook_events(expires_at);
