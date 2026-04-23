CREATE TABLE incidents (
    id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'investigating',
    severity TEXT NOT NULL DEFAULT 'minor',
    started_at TEXT NOT NULL,
    resolved_at TEXT,
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX idx_incidents_org ON incidents(org_id);
CREATE INDEX idx_incidents_status ON incidents(org_id, status);

CREATE TABLE incident_updates (
    id TEXT PRIMARY KEY,
    incident_id TEXT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id),
    status TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    public BOOLEAN NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);

CREATE INDEX idx_incident_updates ON incident_updates(incident_id);

CREATE TABLE incident_status_pages (
    incident_id TEXT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    status_page_id TEXT NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    PRIMARY KEY (incident_id, status_page_id)
);

CREATE TABLE incident_monitors (
    incident_id TEXT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    PRIMARY KEY (incident_id, monitor_id)
);

ALTER TABLE alerts ADD COLUMN incident_id TEXT REFERENCES incidents(id) ON DELETE SET NULL;
