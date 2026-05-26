-- Add metadata column to monitor_checks for storing check-specific data
-- (e.g. resolved DNS records, HTTP response details).
ALTER TABLE monitor_checks ADD COLUMN metadata TEXT NOT NULL DEFAULT '';
