CREATE TABLE IF NOT EXISTS monitor_groups (
    id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_monitor_groups_org ON monitor_groups(org_id);
ALTER TABLE monitors ADD COLUMN group_id TEXT REFERENCES monitor_groups(id) ON DELETE SET NULL;
