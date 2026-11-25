-- Person-first paging: each user keeps their own contact methods (email,
-- SMS, voice, push, chat DMs) and notification rules that say which method
-- to use after how many minutes. A method is paged only once verified.
CREATE TABLE IF NOT EXISTS user_contact_methods (
    id                TEXT PRIMARY KEY,
    org_id            TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    user_id           TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type              TEXT NOT NULL,
    label             TEXT NOT NULL DEFAULT '',
    config            TEXT NOT NULL DEFAULT '{}',
    verified_at       TEXT,
    verify_code_hash  TEXT,
    verify_expires_at TEXT,
    verify_attempts   INTEGER NOT NULL DEFAULT 0,
    last_sent_at      TEXT,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_user_contact_methods_user ON user_contact_methods(user_id);
CREATE INDEX IF NOT EXISTS idx_user_contact_methods_org ON user_contact_methods(org_id);

CREATE TABLE IF NOT EXISTS user_notification_rules (
    id                TEXT PRIMARY KEY,
    org_id            TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    user_id           TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    contact_method_id TEXT NOT NULL REFERENCES user_contact_methods(id) ON DELETE CASCADE,
    delay_minutes     INTEGER NOT NULL DEFAULT 0,
    urgency           TEXT NOT NULL DEFAULT 'any',
    created_at        TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_user_notification_rules_user ON user_notification_rules(user_id);
CREATE INDEX IF NOT EXISTS idx_user_notification_rules_method ON user_notification_rules(contact_method_id);

-- Backfill: every user with a verified account email gets that address as a
-- verified email method plus an immediate rule. Safe to re-run: users who
-- already have an email method, and methods that already have a rule, are
-- skipped.
INSERT INTO user_contact_methods (id, org_id, user_id, type, label, config, verified_at, created_at, updated_at)
SELECT lower(hex(randomblob(16))), u.org_id, u.id, 'email', 'Account email',
       json_object('address', u.email), u.email_verified_at,
       strftime('%Y-%m-%dT%H:%M:%SZ', 'now'), strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
FROM users u
WHERE u.email_verified_at IS NOT NULL AND u.email <> ''
  AND NOT EXISTS (SELECT 1 FROM user_contact_methods m WHERE m.user_id = u.id AND m.type = 'email');

INSERT INTO user_notification_rules (id, org_id, user_id, contact_method_id, delay_minutes, urgency, created_at)
SELECT lower(hex(randomblob(16))), m.org_id, m.user_id, m.id, 0, 'any', strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
FROM user_contact_methods m
WHERE m.type = 'email' AND m.label = 'Account email'
  AND NOT EXISTS (SELECT 1 FROM user_notification_rules r WHERE r.contact_method_id = m.id);
