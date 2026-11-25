-- survey_attempts counts send attempts for the satisfaction survey. The worker
-- stops retrying after a fixed number of failed sends.
ALTER TABLE support_tickets ADD COLUMN survey_attempts INTEGER NOT NULL DEFAULT 0;

-- survey_retry_at holds back a retry after a failed send.
ALTER TABLE support_tickets ADD COLUMN survey_retry_at TEXT;

-- Closed tickets older than the default survey_max_age_days (7) never get a
-- survey. Mark them so enabling SMTP does not mail the backlog. Backfilled
-- rows have survey_sent_at equal to closed_at and survey_attempts = 0.
-- closed_at is RFC3339 UTC text, so the string comparison is chronological.
UPDATE support_tickets
SET survey_sent_at = closed_at
WHERE status = 'closed'
  AND survey_sent_at IS NULL
  AND csat_submitted_at IS NULL
  AND closed_at IS NOT NULL
  AND closed_at < strftime('%Y-%m-%dT%H:%M:%SZ', 'now', '-7 days');
