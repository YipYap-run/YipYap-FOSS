-- Track when the post-close satisfaction survey was emailed.
ALTER TABLE support_tickets ADD COLUMN survey_sent_at TEXT;
