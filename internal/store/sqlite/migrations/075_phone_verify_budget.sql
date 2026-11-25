-- Atomic send-quota buckets for phone verification. One row per
-- fixed-window cap (per number, org and member, hourly and daily) and one
-- per resend-cooldown slot. The reservation is a conditional upsert so
-- concurrent senders cannot overshoot a cap.
CREATE TABLE IF NOT EXISTS phone_verify_budget (
    bucket     TEXT PRIMARY KEY,
    count      INTEGER NOT NULL DEFAULT 0,
    expires_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_phone_verify_budget_expires ON phone_verify_budget(expires_at);
