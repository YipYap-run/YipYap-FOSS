-- Phone number OTP verification. SMS and voice channels (and later per-user
-- contact methods) stay pending until the number's owner enters a code.
CREATE TABLE IF NOT EXISTS phone_verifications (
    id           TEXT PRIMARY KEY,
    org_id       TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    subject_type TEXT NOT NULL,
    subject_id   TEXT NOT NULL,
    phone        TEXT NOT NULL,
    method       TEXT NOT NULL DEFAULT 'sms',
    created_by   TEXT NOT NULL DEFAULT '',
    code_hash    TEXT NOT NULL,
    attempts     INTEGER NOT NULL DEFAULT 0,
    expires_at   TEXT NOT NULL,
    verified_at  TEXT,
    created_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_phone_verifications_subject ON phone_verifications(subject_type, subject_id, created_at);
CREATE INDEX IF NOT EXISTS idx_phone_verifications_org ON phone_verifications(org_id, created_at);
CREATE INDEX IF NOT EXISTS idx_phone_verifications_actor ON phone_verifications(org_id, created_by, created_at);
CREATE INDEX IF NOT EXISTS idx_phone_verifications_phone ON phone_verifications(phone, created_at);

ALTER TABLE notification_channels ADD COLUMN verified_at TEXT;

-- Grandfather phone channels created before verification existed so live
-- paging does not stop on deploy.
UPDATE notification_channels
   SET verified_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
 WHERE type IN ('telnyx_sms', 'telnyx_voice', 'twilio_sms', 'twilio_voice')
   AND verified_at IS NULL;
