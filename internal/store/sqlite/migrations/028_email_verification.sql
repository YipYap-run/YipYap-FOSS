ALTER TABLE users ADD COLUMN email_verified_at                      TEXT;
ALTER TABLE users ADD COLUMN email_verification_sent_at              TEXT;
ALTER TABLE users ADD COLUMN email_verification_resend_count         INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN email_verification_resend_window_started TEXT;
