-- Delivery health per notification channel, written by the notifier after
-- each final outcome. last_error holds a sanitized failure class only.
ALTER TABLE notification_channels ADD COLUMN last_success_at TEXT;
ALTER TABLE notification_channels ADD COLUMN last_failure_at TEXT;
ALTER TABLE notification_channels ADD COLUMN last_error TEXT NOT NULL DEFAULT '';
ALTER TABLE notification_channels ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0;
