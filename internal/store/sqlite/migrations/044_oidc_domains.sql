-- Email domains that route "Sign in with SSO" to an OIDC connection. A domain
-- only routes once the org proves ownership with a DNS TXT record, and a
-- domain can be verified by one org at a time.
CREATE TABLE IF NOT EXISTS oidc_domains (
    id TEXT PRIMARY KEY,
    org_id TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    connection_id TEXT NOT NULL REFERENCES oidc_connections(id) ON DELETE CASCADE,
    domain TEXT NOT NULL,
    verification_token TEXT NOT NULL,
    verified_at TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_oidc_domains_org_domain ON oidc_domains(org_id, domain);
CREATE UNIQUE INDEX IF NOT EXISTS idx_oidc_domains_verified_domain ON oidc_domains(domain) WHERE verified_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_oidc_domains_connection_id ON oidc_domains(connection_id);
