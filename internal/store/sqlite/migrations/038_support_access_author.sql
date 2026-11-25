-- Per-org support ticket visibility for roles below admin. Owners, admins and
-- the ticket opener always have full access. A missing row means 'none'.
-- level: none | see | reply | close
CREATE TABLE IF NOT EXISTS support_ticket_access (
    org_id TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    level TEXT NOT NULL DEFAULT 'none',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (org_id, role)
);

-- Author name captured when a message is written. For staff this is the first
-- name shown to customers; for customers it is the user's name or email.
ALTER TABLE support_messages ADD COLUMN sender_name TEXT;
