-- Owner emails about paid channel types (SMS, voice, Telegram,
-- CloudEvents) after an org leaves its paid plan: one row per org, kind
-- ('grace' when the grace starts, 'disabled' when it ends) and grace
-- deadline. The primary key is the claim, so replicas send each email
-- once, and a later downgrade with a new deadline gets its own emails.
CREATE TABLE IF NOT EXISTS org_paid_channel_notices (
    org_id      TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,
    grace_until TEXT NOT NULL,
    sent_at     TEXT NOT NULL,
    PRIMARY KEY (org_id, kind, grace_until)
);
