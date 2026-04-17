ALTER TABLE monitors ADD COLUMN runbook_url TEXT;
ALTER TABLE monitors ADD COLUMN service_id TEXT;
CREATE INDEX idx_monitors_service_id ON monitors(service_id);

CREATE TABLE services (
    id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    owner_team_id TEXT,
    runbook_url TEXT,
    notes TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX idx_services_org_slug ON services(org_id, slug);
CREATE INDEX idx_services_org_id ON services(org_id);

CREATE TABLE service_links (
    id TEXT PRIMARY KEY,
    service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    label TEXT NOT NULL,
    url_template TEXT NOT NULL,
    icon TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_service_links_service_id ON service_links(service_id);

CREATE TABLE service_dependencies (
    id TEXT PRIMARY KEY,
    service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    depends_on_service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    relationship TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX idx_service_deps_unique ON service_dependencies(service_id, depends_on_service_id);
CREATE INDEX idx_service_deps_service_id ON service_dependencies(service_id);
CREATE INDEX idx_service_deps_depends_on ON service_dependencies(depends_on_service_id);
