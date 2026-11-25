-- Metered SMS and voice sends per org and usage period. Rows exist for
-- every org that sends, with or without a billing row, so promo and comped
-- orgs are counted and capped too. The period key comes from the billing
-- reset time when a paid period is active, else the UTC calendar month.
CREATE TABLE IF NOT EXISTS sms_voice_usage (
    org_id     TEXT NOT NULL,
    period_key TEXT NOT NULL,
    sms_used   INTEGER NOT NULL DEFAULT 0,
    voice_used INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (org_id, period_key)
);

-- One row per usage warning sent (kind 'sms' or 'voice', threshold 80 or
-- 100), so each warning goes out once per org and period.
CREATE TABLE IF NOT EXISTS sms_voice_usage_warnings (
    org_id     TEXT NOT NULL,
    period_key TEXT NOT NULL,
    kind       TEXT NOT NULL,
    threshold  INTEGER NOT NULL,
    sent_at    TEXT NOT NULL,
    PRIMARY KEY (org_id, period_key, kind, threshold)
);
