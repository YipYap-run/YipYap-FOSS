-- When org owners were last emailed that this channel keeps failing. Set
-- by a conditional update so only one replica sends the email for a
-- failure streak; cleared by the next successful delivery.
ALTER TABLE notification_channels ADD COLUMN failure_notified_at TEXT;
