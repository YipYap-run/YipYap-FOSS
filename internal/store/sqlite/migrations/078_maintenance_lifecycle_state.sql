-- Last emitted lifecycle state per maintenance window, shared by every web
-- replica. The ticker claims a transition with a conditional write and only
-- the winner emits, so start and end events fire once per transition. No
-- foreign key: the row must outlive an out-of-band window delete so the
-- ticker can still emit ended-early.
CREATE TABLE IF NOT EXISTS maintenance_lifecycle_state (
    window_id  TEXT PRIMARY KEY,
    org_id     TEXT NOT NULL,
    monitor_id TEXT NOT NULL DEFAULT '',
    active     INTEGER NOT NULL DEFAULT 0,
    changed_at TEXT NOT NULL
);
