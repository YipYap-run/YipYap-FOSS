ALTER TABLE monitors ADD COLUMN latency_warning_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE monitors ADD COLUMN latency_critical_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE monitors ADD COLUMN down_severity TEXT NOT NULL DEFAULT 'critical';
ALTER TABLE monitors ADD COLUMN degraded_severity TEXT NOT NULL DEFAULT 'warning'
