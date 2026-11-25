-- Platform Discord app installations (OAuth2 bot install). One row per org
-- and Discord server (guild). The app posts with the platform bot token, so
-- no per-install secret is stored. Revoked rows keep their history.
CREATE TABLE IF NOT EXISTS discord_installations (
    id           TEXT PRIMARY KEY,
    org_id       TEXT NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    guild_id     TEXT NOT NULL,
    guild_name   TEXT NOT NULL DEFAULT '',
    installed_by TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    revoked_at   TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_discord_installations_org_guild ON discord_installations(org_id, guild_id);
CREATE INDEX IF NOT EXISTS idx_discord_installations_guild ON discord_installations(guild_id);
