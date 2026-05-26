-- 023_monitor_description_autoresolve.sql: Add description and auto_resolve columns to monitors.
ALTER TABLE monitors ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE monitors ADD COLUMN auto_resolve INTEGER NOT NULL DEFAULT 0;
