-- One-time codes that link a Telegram chat to a YipYap channel (or, later,
-- a user's contact method). The UI shows "/link CODE"; the bot binds the
-- chat that sends it. Only a SHA-256 of the code is stored. A row is single
-- use: used_at is set once, and the linked chat is recorded for the UI.
CREATE TABLE IF NOT EXISTS telegram_link_codes (
    id                TEXT PRIMARY KEY,
    code_hash         TEXT NOT NULL UNIQUE,
    org_id            TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    created_by        TEXT NOT NULL,
    purpose           TEXT NOT NULL,
    subject_id        TEXT,
    channel_name      TEXT NOT NULL DEFAULT '',
    expires_at        TEXT NOT NULL,
    used_at           TEXT,
    linked_chat_id    TEXT,
    linked_chat_title TEXT,
    linked_subject_id TEXT,
    created_at        TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_telegram_link_codes_org ON telegram_link_codes(org_id, created_at);
