-- One row per promo expiry warning email (days_before 14, 7 or 1), keyed
-- on the promo expiry so each warning goes out once across replicas and an
-- extended promo gets a fresh set.
CREATE TABLE IF NOT EXISTS promo_expiry_warnings (
    org_id           TEXT NOT NULL,
    promo_expires_at TEXT NOT NULL,
    days_before      INTEGER NOT NULL,
    sent_at          TEXT NOT NULL,
    PRIMARY KEY (org_id, promo_expires_at, days_before)
);
