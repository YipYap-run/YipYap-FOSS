CREATE TABLE IF NOT EXISTS org_settings (
    org_id TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    key    TEXT NOT NULL,
    value  TEXT NOT NULL,
    PRIMARY KEY (org_id, key)
)