ALTER TABLE users ADD COLUMN totp_secret TEXT;
ALTER TABLE users ADD COLUMN mfa_app_enabled BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN totp_backup_codes TEXT;
ALTER TABLE users ADD COLUMN mfa_enforced_at TEXT;

ALTER TABLE orgs ADD COLUMN mfa_required BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE orgs ADD COLUMN mfa_grace_days INTEGER NOT NULL DEFAULT 7;

CREATE TABLE webauthn_credentials (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    org_id TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name TEXT NOT NULL DEFAULT '',
    public_key BLOB NOT NULL,
    attestation_type TEXT NOT NULL DEFAULT 'none',
    sign_count INTEGER NOT NULL DEFAULT 0,
    discoverable BOOLEAN NOT NULL DEFAULT 0,
    user_handle BLOB,
    transports TEXT,
    created_at TEXT NOT NULL
);

CREATE INDEX idx_webauthn_user ON webauthn_credentials(user_id);
CREATE INDEX idx_webauthn_user_handle ON webauthn_credentials(user_handle);
