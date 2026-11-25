-- Verified email change (auth-13): the new address waits here until the
-- confirmation link mailed to it is used. The nonce ties the mailed links to
-- this request, so a newer request voids older links.
ALTER TABLE users ADD COLUMN pending_email TEXT;
ALTER TABLE users ADD COLUMN pending_email_nonce TEXT;
ALTER TABLE users ADD COLUMN pending_email_requested_at TEXT;
