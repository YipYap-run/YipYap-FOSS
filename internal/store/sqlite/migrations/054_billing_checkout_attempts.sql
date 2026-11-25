-- The org's current Stripe Checkout Session. Repeated or parallel upgrade
-- clicks reuse it while it is open, and the attempt number scopes the
-- Stripe idempotency key, so an org never has two payable sessions at
-- once. Times are unix seconds (0 means unset).
CREATE TABLE IF NOT EXISTS billing_checkout_attempts (
    org_id     TEXT PRIMARY KEY,
    attempt    INTEGER NOT NULL,
    session_id TEXT NOT NULL DEFAULT '',
    url        TEXT NOT NULL DEFAULT '',
    seats      INTEGER NOT NULL DEFAULT 0,
    trial_end  BIGINT NOT NULL DEFAULT 0,
    expires_at BIGINT NOT NULL DEFAULT 0
);
