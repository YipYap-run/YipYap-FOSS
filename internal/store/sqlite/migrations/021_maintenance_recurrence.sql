ALTER TABLE maintenance_windows ADD COLUMN recurrence_type TEXT NOT NULL DEFAULT 'none';
ALTER TABLE maintenance_windows ADD COLUMN recurrence_end_at TEXT;
ALTER TABLE maintenance_windows ADD COLUMN days_of_week TEXT NOT NULL DEFAULT '[]';
ALTER TABLE maintenance_windows ADD COLUMN day_of_month INTEGER NOT NULL DEFAULT 0;
