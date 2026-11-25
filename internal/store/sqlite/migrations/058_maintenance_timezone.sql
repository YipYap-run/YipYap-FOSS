-- Maintenance windows carry the IANA zone their times were entered in.
-- Recurrence repeats wall-clock times in that zone, so it follows DST.
-- Existing windows were evaluated in UTC and keep doing so.
ALTER TABLE maintenance_windows ADD COLUMN timezone TEXT NOT NULL DEFAULT 'UTC';
