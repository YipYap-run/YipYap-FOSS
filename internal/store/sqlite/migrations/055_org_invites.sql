-- Member invites. An owner or admin invites an email address with a role;
-- the invitee opens the emailed (or shared) accept link and sets their own
-- password. Only a SHA-256 hash of the link secret is stored. An invite is
-- pending while accepted_at and revoked_at are both NULL, and each org has
-- at most one pending invite per email address (case-insensitive).
CREATE TABLE IF NOT EXISTS org_invites (
    id          TEXT PRIMARY KEY,
    org_id      TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    email       TEXT NOT NULL,
    role        TEXT NOT NULL,
    token_hash  TEXT NOT NULL,
    invited_by  TEXT NOT NULL DEFAULT '',
    expires_at  TEXT NOT NULL,
    accepted_at TEXT,
    revoked_at  TEXT,
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_org_invites_org_id ON org_invites(org_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_org_invites_pending_email
    ON org_invites(org_id, lower(email))
    WHERE accepted_at IS NULL AND revoked_at IS NULL;
