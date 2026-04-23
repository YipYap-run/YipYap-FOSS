CREATE TABLE IF NOT EXISTS billing (
    org_id              TEXT PRIMARY KEY REFERENCES orgs(id),
    stripe_customer_id  TEXT UNIQUE,
    stripe_subscription_id TEXT UNIQUE,
    billing_email       TEXT,
    seat_count          INTEGER NOT NULL DEFAULT 1,
    has_enterprise      INTEGER NOT NULL DEFAULT 0,
    period_end          TEXT,
    status              TEXT NOT NULL DEFAULT 'active',
    sms_used            INTEGER NOT NULL DEFAULT 0,
    voice_used          INTEGER NOT NULL DEFAULT 0,
    sms_voice_reset_at  TEXT
);

CREATE INDEX IF NOT EXISTS idx_billing_stripe_customer ON billing(stripe_customer_id);
CREATE INDEX IF NOT EXISTS idx_billing_status ON billing(status);
