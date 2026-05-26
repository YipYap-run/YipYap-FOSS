CREATE TABLE IF NOT EXISTS oidc_signing_keys (
    kid               TEXT PRIMARY KEY,
    algorithm         TEXT NOT NULL,
    encrypted_private BLOB NOT NULL,
    public_jwk        TEXT NOT NULL,
    status            TEXT NOT NULL,
    created_at        TEXT NOT NULL,
    activated_at      TEXT,
    retired_at        TEXT
);

CREATE INDEX IF NOT EXISTS idx_oidc_signing_keys_status
    ON oidc_signing_keys(status);

CREATE INDEX IF NOT EXISTS idx_oidc_signing_keys_retired_at
    ON oidc_signing_keys(retired_at);
