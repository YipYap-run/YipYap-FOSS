-- Enforce SSO: when set, org members other than the owner must sign in
-- through the org's SSO connection. Password login, passkey login and
-- password reset are refused for them.
ALTER TABLE orgs ADD COLUMN sso_enforced BOOLEAN NOT NULL DEFAULT 0;
