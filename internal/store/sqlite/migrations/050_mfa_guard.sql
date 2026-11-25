-- Shared second-factor state so checks hold across web replicas (auth-14,
-- auth-15). Times are unix seconds.
--
-- mfa_guard: last accepted TOTP step (replay guard) and the per-account
-- failed attempt counter and lockout.
CREATE TABLE IF NOT EXISTS mfa_guard (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    totp_last_step INTEGER,
    failed_count INTEGER NOT NULL DEFAULT 0,
    window_start INTEGER,
    locked_until INTEGER
);

-- webauthn_challenges: one-time WebAuthn challenge sessions with a short
-- TTL, swept by the account cleanup job.
CREATE TABLE IF NOT EXISTS webauthn_challenges (
    id TEXT PRIMARY KEY,
    data TEXT NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_webauthn_challenges_expires_at ON webauthn_challenges(expires_at);
