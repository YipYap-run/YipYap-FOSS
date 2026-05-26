-- Composite index for efficient status-filtered check queries and counts.
CREATE INDEX IF NOT EXISTS idx_monitor_checks_status ON monitor_checks(monitor_id, status, checked_at DESC);
