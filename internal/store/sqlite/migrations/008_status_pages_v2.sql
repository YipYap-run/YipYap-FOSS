ALTER TABLE status_pages ADD COLUMN created_at TEXT;
ALTER TABLE status_pages ADD COLUMN updated_at TEXT;

CREATE TABLE IF NOT EXISTS status_page_groups (
    id               TEXT PRIMARY KEY,
    status_page_id   TEXT NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    position         INTEGER NOT NULL DEFAULT 0,
    default_expanded INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_spg_page ON status_page_groups(status_page_id);

DROP TABLE IF EXISTS status_page_monitors;
CREATE TABLE IF NOT EXISTS status_page_monitors (
    id              TEXT PRIMARY KEY,
    group_id        TEXT REFERENCES status_page_groups(id) ON DELETE CASCADE,
    status_page_id  TEXT NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    monitor_id      TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    display_name    TEXT NOT NULL DEFAULT '',
    position        INTEGER NOT NULL DEFAULT 0,
    show_status     INTEGER NOT NULL DEFAULT 1,
    show_uptime_bar INTEGER NOT NULL DEFAULT 1,
    uptime_periods  TEXT NOT NULL DEFAULT '24h,90d',
    show_latency    INTEGER NOT NULL DEFAULT 0,
    show_checks     INTEGER NOT NULL DEFAULT 0,
    show_incidents  INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_spm_page ON status_page_monitors(status_page_id);
CREATE INDEX IF NOT EXISTS idx_spm_group ON status_page_monitors(group_id);
