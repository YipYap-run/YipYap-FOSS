-- Heartbeat monitors created by bulk import had no token, and the heartbeat
-- endpoint now rejects any monitor whose stored token is empty. Give each of
-- them a fresh random token so they can report again. Safe to re-run: only
-- empty tokens are touched.
UPDATE monitors
SET heartbeat_token = lower(hex(randomblob(16)))
WHERE type = 'heartbeat' AND (heartbeat_token IS NULL OR heartbeat_token = '');
